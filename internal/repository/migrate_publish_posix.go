//go:build !windows

package repository

import (
	"errors"
	"path/filepath"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func syncMigrationDirectory(path string) (err error) {
	a, err := anchorPath(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	f, err := a.openDirectory()
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func publishMigration(source, destination string) error {
	directories, err := syncMigrationFiles(source)
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := syncMigrationDirectory(directories[i]); err != nil {
			return err
		}
	}
	if err := renameMigrationExclusive(source, destination); err != nil {
		return err
	}
	// A failed barrier must not acknowledge durability or remove the complete
	// destination. Leave it intact for inspection after uncertain publication.
	if err := syncMigrationDirectory(filepath.Dir(source)); err != nil {
		return errors.Join(storev2.ErrPublicationUncertain, err)
	}
	if err := syncMigrationDirectory(filepath.Dir(destination)); err != nil {
		return errors.Join(storev2.ErrPublicationUncertain, err)
	}
	return nil
}
