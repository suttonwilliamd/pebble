package repository

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func linkReparseFixture(substitute, display string, flags uint32) []byte {
	sub, print := utf16.Encode([]rune(substitute)), utf16.Encode([]rune(display))
	b := make([]byte, 20+2*(len(sub)+len(print)))
	binary.LittleEndian.PutUint32(b, windows.IO_REPARSE_TAG_SYMLINK)
	binary.LittleEndian.PutUint16(b[4:], uint16(len(b)-8))
	binary.LittleEndian.PutUint16(b[10:], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(b[12:], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(b[14:], uint16(2*len(print)))
	binary.LittleEndian.PutUint32(b[16:], flags)
	for i, u := range sub {
		binary.LittleEndian.PutUint16(b[20+2*i:], u)
	}
	for i, u := range print {
		binary.LittleEndian.PutUint16(b[20+2*(i+len(sub)):], u)
	}
	return b
}
func TestFilesystemWindowsTargetDecoding(t *testing.T) {
	const attrs = windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_DIRECTORY
	for _, tc := range []struct {
		name, sub, display, want string
		flags                    uint32
	}{
		{"mismatched-display", `MiXeD\..\café\é\😀`, `WRONG\display`, `MiXeD\..\café\é\😀`, 1},
		{"empty-display", `relative\dangling`, "", `relative\dangling`, 1},
		{"drive", `\??\c:\MiXeD\..\café`, `not-the-target`, `c:\MiXeD\..\café`, 0},
		{"unc", `\??\UNC\SeRvEr\ShArE\..\é`, "", `\\SeRvEr\ShArE\..\é`, 0},
		{"volume", `\??\Volume{01234567-89ab-CDEF-0123-456789abcdef}\MiXeD\..\é`, "", `\\?\Volume{01234567-89ab-CDEF-0123-456789abcdef}\MiXeD\..\é`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeWindowsLink(linkReparseFixture(tc.sub, tc.display, tc.flags), attrs)
			if err != nil || got.target != tc.want || !got.directory {
				t.Fatalf("authoritative target/type: %+v %v", got, err)
			}
		})
	}
	for _, tc := range []struct {
		name, sub string
		flags     uint32
	}{
		{"relative-flag-with-absolute", `\??\C:\file`, 1},
		{"full-flag-with-relative", `relative`, 0},
		{"unknown-flags", `relative`, 2},
		{"unknown-nt-namespace", `\Device\HarddiskVolume1\file`, 0},
		{"unknown-dos-device", `\??\GLOBALROOT\Device\file`, 0},
		{"stateful-drive", `C:relative`, 1},
		{"root-relative", `\root-relative`, 1},
		{"posix-separators", `dir/leaf`, 1},
		{"unc-without-share", `\??\UNC\server`, 0},
		{"invalid-volume", `\??\Volume{not-a-guid}\leaf`, 0},
		{"empty-target", "", 1},
		{"nul-target", "a\x00b", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := decodeWindowsLink(linkReparseFixture(tc.sub, "untrusted display", tc.flags), attrs); err == nil {
				t.Fatalf("accepted malformed/unsupported target: %+v", got)
			}
		})
	}
	for _, damage := range []string{"tag", "length", "odd-buffer", "sub-offset", "sub-length", "sub-bounds", "print-offset", "print-length", "print-bounds", "high-surrogate", "low-surrogate", "print-surrogate", "attributes"} {
		t.Run(damage, func(t *testing.T) {
			b := linkReparseFixture("target", "display", 1)
			a := uint32(attrs)
			switch damage {
			case "tag":
				binary.LittleEndian.PutUint32(b, windows.IO_REPARSE_TAG_MOUNT_POINT)
			case "length":
				binary.LittleEndian.PutUint16(b[4:], uint16(len(b)))
			case "odd-buffer":
				b = append(b, 0)
				binary.LittleEndian.PutUint16(b[4:], uint16(len(b)-8))
			case "sub-offset":
				binary.LittleEndian.PutUint16(b[8:], 1)
			case "sub-length":
				binary.LittleEndian.PutUint16(b[10:], 3)
			case "sub-bounds":
				binary.LittleEndian.PutUint16(b[10:], 0xfffe)
			case "print-offset":
				binary.LittleEndian.PutUint16(b[12:], 1)
			case "print-length":
				binary.LittleEndian.PutUint16(b[14:], 3)
			case "print-bounds":
				binary.LittleEndian.PutUint16(b[14:], 0xfffe)
			case "high-surrogate":
				binary.LittleEndian.PutUint16(b[20:], 0xd800)
			case "low-surrogate":
				binary.LittleEndian.PutUint16(b[20:], 0xdc00)
			case "print-surrogate":
				binary.LittleEndian.PutUint16(b[32:], 0xd800)
			case "attributes":
				a = windows.FILE_ATTRIBUTE_DIRECTORY
			}
			if got, err := decodeWindowsLink(b, a); err == nil {
				t.Fatalf("accepted malformed reparse buffer: %+v", got)
			}
		})
	}
	t.Log("generated reparse-buffer decoding proof; not native link-creation proof")
}
func TestFilesystemWindowsTargetNativeRoundtrip(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !requireFixtureLinks(t, r.root) {
		return
	}
	committed(t, r, "empty")
	if err = r.CreateBranch("empty"); err != nil {
		t.Fatal(err)
	}
	targets := []string{`MiXeD\..\café\é\😀`, filepath.Join(r.root, "unused") + `\..\MiXeD`, `\\NoSuchServer.invalid\NoSuchShare\MiXeD`, `\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\MiXeD`}
	for i, target := range targets {
		name := []string{"relative", "drive", "unc", "volume"}[i]
		if err = createLink(target, filepath.Join(r.root, name), i%2 == 1); err != nil {
			t.Fatalf("native literal fixture %s: %v", name, err)
		}
		link, err := readLink(filepath.Join(r.root, name))
		if err != nil || link.target != target || link.directory != (i%2 == 1) {
			t.Fatalf("native authoritative target/flags %s: %+v %v", name, link, err)
		}
	}
	committed(t, r, "native targets")
	if err = r.CreateBranch("links"); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("empty"); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("links"); err != nil {
		t.Fatal(err)
	}
	r, err = Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range targets {
		name := []string{"relative", "drive", "unc", "volume"}[i]
		if err = os.Remove(filepath.Join(r.root, name)); err != nil {
			t.Fatal(err)
		}
		if err = r.Restore(name); err != nil {
			t.Fatal(err)
		}
		link, err := readLink(filepath.Join(r.root, name))
		if err != nil || link.target != target || link.directory != (i%2 == 1) {
			t.Fatalf("native restored target/flags %s: %+v %v", name, link, err)
		}
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
}
