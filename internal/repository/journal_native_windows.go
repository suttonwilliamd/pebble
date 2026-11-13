package repository

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func renameMigrationExclusive(source, target string) error {
	return moveJournalMetadata(source, target, false)
}

// Private metadata only; public recovery publication always uses anchoredMove
// without replacement. This matches the store's write-through ref convention.
func replaceRecoveryIntent(source, target string) error {
	return moveJournalMetadata(source, target, true)
}

func moveJournalMetadata(source, target string, replace bool) error {
	from, err := windows.UTF16PtrFromString(`\\?\` + source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(`\\?\` + target)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(from, to, flags)
}

func syncMigrationDirectory(path string) (err error) {
	a, err := anchorPath(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	h, err := relativeHandle(a.parent, a.leaf, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("directory barrier requires an ordinary no-follow directory: %s", path)
	}
	return windows.FlushFileBuffers(h)
}
