package repository

import (
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
		// POSIX permits fsync through a read-only descriptor (including 0444
		// preimages); Windows still requires writable FlushFileBuffers handles.
		return syncOriginal(path)
	})
	return directories, err
}
