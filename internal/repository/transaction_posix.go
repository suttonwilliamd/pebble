//go:build !windows

package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func filesystemIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("filesystem identity unavailable: %s", path)
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
func moveWorktree(source, target string, barrier func() error) error {
	if err := renameMigrationExclusive(source, target); err != nil {
		return err
	}
	if err := barrier(); err != nil {
		return err
	}
	if err := syncMigrationDirectory(filepath.Dir(source)); err != nil {
		return err
	}
	return syncMigrationDirectory(filepath.Dir(target))
}

func settleWorktree(ctx context.Context, r *Repository, stage string, plan *worktreeIntent) error {
	for i := len(plan.Directories) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := syncMigrationDirectory(filepath.Join(r.root, filepath.FromSlash(plan.Directories[i].Path))); err != nil {
			return err
		}
	}
	for _, path := range []string{r.root, stage, filepath.Dir(stage)} {
		if err := syncMigrationDirectory(path); err != nil {
			return err
		}
	}
	return nil
}
