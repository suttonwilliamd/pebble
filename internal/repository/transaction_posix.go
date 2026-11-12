//go:build !windows

package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func syncOriginal(path string) (err error) {
	a, err := anchorPath(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// A symlink cannot be opened for fsync without dereferencing it.
		// Its literal metadata is persisted by the pinned parent barrier.
		return syscall.Fsync(a.parent)
	}
	f, err := a.openRead()
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

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

func settleWorktree(ctx context.Context, r *Repository, stage string, plan *worktreeIntent) error {
	for i := len(plan.Directories) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(r.root, filepath.FromSlash(plan.Directories[i].Path))
		if plan.Directories[i].Remove {
			path = filepath.Join(stage, retainedDirectoryName(i))
		}
		if err := syncMigrationDirectory(path); err != nil {
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
