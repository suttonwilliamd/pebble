package repository

import (
	"errors"

	"github.com/suttonwilliamd/pebble/internal/storev2"
	"golang.org/x/sys/windows"
)

func publishMigration(source, destination string) error {
	if _, err := syncMigrationFiles(source); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(`\\?\` + source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(`\\?\` + destination)
	if err != nil {
		return err
	}
	// No REPLACE_EXISTING, no copy/delete fallback. Store initialization has
	// already enforced fixed local NTFS. A directory move uses the same
	// write-through primitive as the store's namespace publication.
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return errors.Join(storev2.ErrPublicationUncertain, err)
	}
	return nil
}
