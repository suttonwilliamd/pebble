//go:build !windows

package repository

import (
	"os"
	"path/filepath"
)

// Private metadata only, while the persistent worktree guard is held. Never
// use replacement publication for a working-tree or public recovery path.
func replaceRecoveryIntent(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return syncMigrationDirectory(filepath.Dir(target))
}
