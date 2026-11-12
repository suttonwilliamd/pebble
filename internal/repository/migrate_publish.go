package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Only private generated destination data is opened for sync. Source paths are
// never passed here. Directories are returned parent-first for POSIX barriers.
func syncMigrationFiles(root string) ([]string, error) {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if _, err = readLink(path); err != nil {
				return err
			}
			return syncOriginal(path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular migration stage file: %s", path)
		}
		// Windows FlushFileBuffers requires write access. These are our own
		// newly created files; no write is made through this handle.
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		return errors.Join(f.Sync(), f.Close())
	})
	return directories, err
}
