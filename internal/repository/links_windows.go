package repository

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

const measuresExecutable = false
const measuresDirectoryLink = true

func (a *pathAnchor) readLink() (_ linkContent, err error) {
	h, err := relativeHandle(a.parent, a.leaf, windows.GENERIC_READ|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, 0)
	if err != nil {
		return linkContent{}, err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	return readLinkHandle(h)
}
func readLinkHandle(h windows.Handle) (linkContent, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return linkContent{}, err
	}
	buffer := worktreeBuffers.Get().(*[maxLinkBytes + 1]byte)
	defer worktreeBuffers.Put(buffer)
	b := buffer[:windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE]
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &b[0], uint32(len(b)), &n, nil); err != nil {
		return linkContent{}, err
	}
	if n > uint32(len(b)) {
		return linkContent{}, fmt.Errorf("invalid symbolic-link buffer length")
	}
	return decodeWindowsLink(b[:n], info.FileAttributes)
}

// MS-FSCC: SubstituteName identifies the target. PrintName is only display
// metadata. This is the API-view target, not forensic reparse-buffer storage.
func decodeWindowsLink(b []byte, attributes uint32) (linkContent, error) {
	if len(b) < 20 || len(b) > windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE || len(b)%2 != 0 || binary.LittleEndian.Uint32(b) != windows.IO_REPARSE_TAG_SYMLINK || int(binary.LittleEndian.Uint16(b[4:6]))+8 != len(b) || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return linkContent{}, fmt.Errorf("unsupported symbolic-link tag/framing")
	}
	flags := binary.LittleEndian.Uint32(b[16:20])
	if flags > 1 {
		return linkContent{}, fmt.Errorf("unsupported symbolic-link flags %#x", flags)
	}
	var target string
	for field := 0; field < 2; field++ {
		start := 8 + field*4
		offset, length := int(binary.LittleEndian.Uint16(b[start:])), int(binary.LittleEndian.Uint16(b[start+2:]))
		if offset%2 != 0 || length%2 != 0 || 20+offset+length > len(b) {
			return linkContent{}, fmt.Errorf("invalid symbolic-link name framing")
		}
		name, err := decodeLinkUTF16(b[20+offset : 20+offset+length])
		if err != nil {
			return linkContent{}, err
		}
		if field == 0 {
			target = name
		}
	}
	if flags == 0 {
		if !strings.HasPrefix(target, `\??\`) {
			return linkContent{}, fmt.Errorf("unsupported absolute NT symbolic-link namespace")
		}
		tail := target[4:]
		switch {
		case windowsDriveAbsolute(tail):
			target = tail
		case strings.HasPrefix(tail, `UNC\`):
			target = `\\` + tail[4:]
		case windowsVolumeAbsolute(tail):
			target = `\\?\` + tail
		default:
			return linkContent{}, fmt.Errorf("unsupported absolute NT symbolic-link namespace")
		}
	}
	absolute, err := windowsTarget(target)
	if err != nil {
		return linkContent{}, err
	}
	if absolute != (flags == 0) {
		return linkContent{}, fmt.Errorf("symbolic-link target/relative-flag disagreement")
	}
	return linkContent{target: target, directory: attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0}, nil
}
func decodeLinkUTF16(data []byte) (string, error) {
	var out strings.Builder
	out.Grow(len(data) * 3 / 2)
	for i := 0; i < len(data); i += 2 {
		unit := binary.LittleEndian.Uint16(data[i:])
		r := rune(unit)
		if unit >= 0xd800 && unit <= 0xdbff {
			if i+4 > len(data) {
				return "", fmt.Errorf("unrepresentable symbolic-link UTF-16")
			}
			low := binary.LittleEndian.Uint16(data[i+2:])
			if low < 0xdc00 || low > 0xdfff {
				return "", fmt.Errorf("unrepresentable symbolic-link UTF-16")
			}
			r = utf16.DecodeRune(r, rune(low))
			i += 2
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			return "", fmt.Errorf("unrepresentable symbolic-link UTF-16")
		}
		out.WriteRune(r)
	}
	return out.String(), nil
}
func windowsDriveAbsolute(target string) bool {
	return len(target) >= 3 && ((target[0] >= 'A' && target[0] <= 'Z') || (target[0] >= 'a' && target[0] <= 'z')) && target[1] == ':' && target[2] == '\\'
}
func windowsVolumeAbsolute(target string) bool {
	if len(target) < 45 || !strings.EqualFold(target[:7], "Volume{") || target[43] != '}' || target[44] != '\\' {
		return false
	}
	for i, c := range target[7:43] {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
func windowsTarget(target string) (absolute bool, err error) {
	if err = validLinkTarget(target); err != nil {
		return false, err
	}
	unsupported := func() (bool, error) {
		return false, fmt.Errorf("unsupported Windows symbolic-link target interpretation %q (no target lookup or normalization)", target)
	}
	if !utf8.ValidString(target) || strings.ContainsRune(target, '/') {
		return unsupported()
	}
	switch {
	case windowsDriveAbsolute(target):
		if strings.ContainsRune(target[2:], ':') {
			return unsupported()
		}
		return true, nil
	case strings.HasPrefix(target, `\\?\`):
		if !windowsVolumeAbsolute(target[4:]) || strings.ContainsRune(target[4:], ':') {
			return unsupported()
		}
		return true, nil
	case strings.HasPrefix(target, `\\`):
		rest := target[2:]
		split := strings.IndexByte(rest, '\\')
		if split <= 0 || rest[:split] == "." || rest[:split] == "?" || split+1 == len(rest) || rest[split+1] == '\\' || strings.ContainsRune(rest, ':') {
			return unsupported()
		}
		return true, nil
	case strings.HasPrefix(target, `\`) || strings.ContainsRune(target, ':'):
		return unsupported()
	default:
		return false, nil
	}
}
func validateMaterializableLink(target string) error { _, err := windowsTarget(target); return err }
func createLink(target, path string, directory bool) error {
	if !utf8.ValidString(target) {
		return fmt.Errorf("unrepresentable symbolic-link UTF-8")
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Extended absolute spelling prevents Win32 dot/separator preprocessing.
	// The decoder reverses this OS namespace prefix, never target components.
	if windowsDriveAbsolute(target) {
		target = `\\?\` + target
	} else if strings.HasPrefix(target, `\\`) && !strings.HasPrefix(target, `\\?\`) {
		target = `\\?\UNC\` + target[2:]
	}
	t, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// Request existing capability only; never enable Developer Mode or privileges.
	flags := uint32(0x2)
	if directory {
		flags |= windows.SYMBOLIC_LINK_FLAG_DIRECTORY
	}
	return windows.CreateSymbolicLink(p, t, flags)
}
