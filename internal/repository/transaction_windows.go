package repository

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
)

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
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("reparse point: %s", path)
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
func moveWorktree(source, target string, barrier func() error) error {
	from, err := windows.UTF16PtrFromString(`\\?\` + source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(`\\?\` + target)
	if err != nil {
		return err
	}
	if err := barrier(); err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

func settleWorktree(ctx context.Context, r *Repository, stage string, plan *worktreeIntent) error {
	// Re-establish installed-file namespace barriers on retry by moving the same
	// identity through its recorded stage slot. Either interruption state remains
	// representable; no replacement, copy/delete fallback or directory Sync no-op.
	for i, file := range plan.Files {
		if file.Keep || file.Target == nil {
			continue
		}
		path, err := r.safePath(file.Path)
		if err != nil {
			return err
		}
		temporary := filepath.Join(stage, stageName(i))
		if err = r.transactionMove(ctx, "stabilize-stage", path, temporary); err != nil {
			return err
		}
		if err = r.transactionMove(ctx, "stabilize-install", temporary, path); err != nil {
			return err
		}
	}
	return nil
}
