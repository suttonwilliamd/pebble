package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func syncOriginal(path string) (err error) {
	a, err := anchorPath(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	h, err := relativeHandle(a.parent, a.leaf, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, 0)
	if err != nil {
		return err
	}
	return errors.Join(windows.FlushFileBuffers(h), windows.CloseHandle(h))
}

func filesystemIdentity(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(`\\?\` + path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	err = errors.Join(err, windows.CloseHandle(h))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func settleWorktree(ctx context.Context, r *Repository, stage string, plan *worktreeIntent) error {
	for _, file := range plan.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.Target == nil {
			continue
		}
		path, err := r.safePath(file.Path)
		if err != nil {
			return err
		}
		if err = syncOriginal(path); err != nil {
			return err
		}
	}
	for i, dir := range plan.Directories {
		path := filepath.Join(r.root, filepath.FromSlash(dir.Path))
		if dir.Remove {
			path = filepath.Join(stage, retainedDirectoryName(i))
		}
		if err := syncOriginal(path); err != nil {
			return err
		}
	}
	for _, path := range []string{r.root, stage, filepath.Dir(stage)} {
		if err := syncOriginal(path); err != nil {
			return err
		}
	}
	return nil
}
