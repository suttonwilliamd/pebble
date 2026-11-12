package storev2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InitRepository initializes only new metadata, preserving all existing working
// files. Init retains its stricter empty-directory library contract. Neither
// initializer upgrades, repairs or overwrites existing/partial metadata.
func InitRepository(ctx context.Context, root string) (_ *Store, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = directory(root); err != nil {
		return nil, err
	}
	if err = checkPlatform(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if name == ".pebble" || strings.HasPrefix(name, ".pebble-init") {
			return nil, fmt.Errorf("existing or incomplete Pebble metadata: %s; no implicit migration", e.Name())
		}
	}
	lockPath := filepath.Join(root, ".pebble-init.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, lock.Close(), os.Remove(lockPath)) }()
	stage, err := os.MkdirTemp(root, ".pebble-init-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	s, err := Init(ctx, stage)
	if err != nil {
		return nil, err
	}
	if err = s.UpdateHead(ctx, "", "heads/main"); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	target := filepath.Join(root, ".pebble")
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return nil, err
	}
	if err = publishDirectory(filepath.Join(stage, ".pebble"), target); err != nil {
		return nil, errors.Join(ErrPublicationUncertain, err)
	}
	return Open(root)
}
