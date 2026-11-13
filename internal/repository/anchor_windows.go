package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pin every ancestor without delete sharing. Each native open resolves exactly
// one component relative to its no-follow parent, never through a reparse point.
type pathAnchor struct {
	handles    []windows.Handle
	parent     windows.Handle
	leaf, path string
}

func anchorPath(path string, writable bool) (_ *pathAnchor, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, fmt.Errorf("anchored access requires a local drive: %s", path)
	}
	a := &pathAnchor{path: path}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.close())
		}
	}()
	root, err := windows.UTF16PtrFromString(`\\?\` + volume + `\`)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(root, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	a.handles = append(a.handles, h)
	parts := strings.Split(strings.TrimPrefix(path[len(volume):], `\`), `\`)
	for i, part := range parts[:len(parts)-1] {
		access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
		if writable && i == len(parts)-2 {
			access |= windows.GENERIC_WRITE
		}
		h, err = relativeHandle(h, part, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_DIRECTORY_FILE)
		if err != nil {
			return nil, err
		}
		a.handles = append(a.handles, h)
		var info windows.ByHandleFileInformation
		if err = windows.GetFileInformationByHandle(h, &info); err != nil {
			return nil, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, fmt.Errorf("reparse ancestor: %s", part)
		}
	}
	a.parent, a.leaf = h, parts[len(parts)-1]
	return a, nil
}
func relativeHandle(parent windows.Handle, name string, access, share, options uint32) (windows.Handle, error) {
	text, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: text}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, access, &oa, &iosb, nil, 0, share, windows.FILE_OPEN, options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			return 0, status.Errno()
		}
		return 0, err
	}
	return h, nil
}
func (a *pathAnchor) close() (err error) {
	for i := len(a.handles) - 1; i >= 0; i-- {
		err = errors.Join(err, windows.CloseHandle(a.handles[i]))
	}
	return err
}
func (a *pathAnchor) openRead() (*os.File, error) {
	h, err := relativeHandle(a.parent, a.leaf, windows.GENERIC_READ|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		if err == nil {
			err = fmt.Errorf("refusing reparse file: %s", a.leaf)
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), a.path), nil
}

// Rename the opened source identity, not a subsequently substituted name.
// Writable handles are required by FlushFileBuffers, including directories.
// No replacement, copy/delete fallback or suppressed barrier failures.
func anchoredMove(source, target, identity string, image *fileImage, _ bool, check, before, after, parents, barrier func() error) (err error) {
	from, err := anchorPath(source, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, from.close()) }()
	to, err := anchorPath(target, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, to.close()) }()
	if err = check(); err != nil {
		return err
	}
	h, err := relativeHandle(from.parent, from.leaf, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, 0)
	if err != nil {
		return fmt.Errorf("opening owned rename source %s: %w", source, err)
	}
	f := os.NewFile(uintptr(h), source)
	defer func() { err = errors.Join(err, f.Close()) }()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	if actual != identity {
		return fmt.Errorf("source identity changed: %s", source)
	}
	if image != nil {
		if image.Symlink {
			link, e := readLinkHandle(h)
			if e != nil {
				return e
			}
			if err = verifyLinkImage(link, image); err != nil {
				return err
			}
		} else if err = verifyOpenedImage(f, image); err != nil {
			return err
		}
	}
	type renameInfo struct {
		Replace uint32
		Root    windows.Handle
		Length  uint32
		Name    [1]uint16
	}
	name, err := windows.UTF16FromString(to.leaf)
	if err != nil {
		return err
	}
	var layout renameInfo
	offset := int(unsafe.Offsetof(layout.Name))
	size := offset + (len(name)-1)*2
	if minimum := int(unsafe.Sizeof(layout)); size < minimum {
		size = minimum
	}
	data := make([]byte, size)
	value := (*renameInfo)(unsafe.Pointer(&data[0]))
	value.Root = to.parent
	value.Length = uint32((len(name) - 1) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&data[offset])), len(name)-1), name[:len(name)-1])
	if err = barrier(); err != nil {
		return err
	}
	if err = before(); err != nil {
		return err
	}
	var iosb windows.IO_STATUS_BLOCK
	if err = windows.NtSetInformationFile(h, &iosb, &data[0], uint32(len(data)), windows.FileRenameInformation); err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		return fmt.Errorf("renaming owned handle %s to %s: %w", source, target, err)
	}
	if err = after(); err != nil {
		return err
	}
	if err = parents(); err != nil {
		return err
	}
	return errors.Join(windows.FlushFileBuffers(h), windows.FlushFileBuffers(from.parent), windows.FlushFileBuffers(to.parent))
}

// Reestablish both writable-handle barriers after a consumed restoration.
func anchoredBarrier(path, identity string, image *fileImage, check, barrier func() error) (err error) {
	a, err := anchorPath(path, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	if err = check(); err != nil {
		return err
	}
	h, err := relativeHandle(a.parent, a.leaf, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), path)
	defer func() { err = errors.Join(err, f.Close()) }()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	if actual != identity {
		return fmt.Errorf("restored identity changed: %s", path)
	}
	if image == nil {
		if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("restored directory became a non-directory or reparse point: %s", path)
		}
	} else if image.Symlink {
		link, err := readLinkHandle(h)
		if err != nil {
			return err
		}
		if err = verifyLinkImage(link, image); err != nil {
			return err
		}
	} else if err = verifyOpenedImage(f, image); err != nil {
		return err
	}
	if err = barrier(); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	return errors.Join(windows.FlushFileBuffers(h), windows.FlushFileBuffers(a.parent), check())
}
