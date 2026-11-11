package storev2

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// No directory-Sync no-op and no copy/delete fallback. Windows publication uses
// MoveFileExW(MOVEFILE_WRITE_THROUGH) on fixed local NTFS only, after file Sync.
// OS/filesystem/hardware guarantees still require independent crash testing.
func checkPlatform(root string) error {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	var volume [32768]uint16
	if err = windows.GetVolumePathName(path, &volume[0], uint32(len(volume))); err != nil {
		return err
	}
	if windows.GetDriveType(&volume[0]) != windows.DRIVE_FIXED {
		return fmt.Errorf("v2 durability requires a fixed local NTFS volume")
	}
	var filesystem [32]uint16
	if err = windows.GetVolumeInformation(&volume[0], nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
		return err
	}
	if windows.UTF16ToString(filesystem[:]) != "NTFS" {
		return fmt.Errorf("v2 durability requires NTFS, got %s", windows.UTF16ToString(filesystem[:]))
	}
	return nil
}

func nativePath(path string) (*uint16, error) {
	if !strings.HasPrefix(path, `\\?\`) {
		path = `\\?\` + path
	}
	return windows.UTF16PtrFromString(path)
}
func moveThrough(source, target string, replace bool) error {
	from, err := nativePath(source)
	if err != nil {
		return err
	}
	to, err := nativePath(target)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(from, to, flags)
}
func publishFile(source, target string, fault func(string, string) error) (bool, error) {
	if fault != nil {
		if err := fault("namespace-sync", target); err != nil {
			return false, err
		}
	}
	// A failed OS rename can have an uncertain outcome. Report that uncertainty
	// rather than assuming it is safe to undo a possibly installed complete ref.
	return true, moveThrough(source, target, true)
}
func publishDirectory(source, target string) error { return moveThrough(source, target, false) }
func makeDurableDirectory(path string) error {
	temporary, err := os.MkdirTemp(filepath.Dir(path), tempPrefix)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return err
	}
	return publishDirectory(temporary, path)
}
