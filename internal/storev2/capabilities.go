package storev2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/suttonwilliamd/pebble/internal/processlock"
)

// Existing object envelopes and IDs are unchanged. This repository-level gate
// excludes binaries that only understand regular/executable tree entries.
const filesystemMarker = "pebble-format 2\nrequires filesystem-v1\n"

func (s *Store) marker() (_ string, err error) {
	f, err := regular(filepath.Join(s.meta, "format"))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var data [len(filesystemMarker) + 1]byte
	n, readErr := io.ReadFull(f, data[:])
	if readErr != io.ErrUnexpectedEOF {
		return "", errors.Join(ErrFormat, readErr)
	}
	if n == len(formatMarker) && bytes.Equal(data[:n], []byte(formatMarker)) {
		return formatMarker, nil
	}
	if n == len(filesystemMarker) && bytes.Equal(data[:n], []byte(filesystemMarker)) {
		return filesystemMarker, nil
	}
	return "", ErrFormat
}

// FilesystemEnabled checks persistent capability metadata, never a cached Open.
func (s *Store) FilesystemEnabled() (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	marker, err := s.marker()
	return marker == filesystemMarker, err
}
func (s *Store) requireFilesystem() error {
	enabled, err := s.FilesystemEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("symbolic-link entries require explicit pebble enable-filesystem; repository was not upgraded")
	}
	return nil
}

// EnableFilesystem is explicit and idempotent. Lock order matches repository
// mutations: worktree guard, then store writer guard. Old open handles use these
// same persistent guard files and revalidate the marker under their writer lock.
// Never replace/unlink guard files: doing so would split ownership across inodes.
func (s *Store) EnableFilesystem(ctx context.Context) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.guard(); err != nil {
		return err
	}
	for _, name := range []string{"worktree.lock", "worktree-journal"} {
		if _, e := os.Lstat(filepath.Join(s.meta, name)); !os.IsNotExist(e) {
			return errors.Join(fmt.Errorf("capability enablement refused: preserve/resolve %s first", name), e)
		}
	}
	unlock, err := processlock.Acquire(filepath.Join(s.meta, "worktree.guard"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	// Repeat pending-state checks while holding the existing worktree guard.
	for _, name := range []string{"worktree.lock", "worktree-journal"} {
		if _, e := os.Lstat(filepath.Join(s.meta, name)); !os.IsNotExist(e) {
			return errors.Join(fmt.Errorf("capability enablement refused: preserve/resolve %s first", name), e)
		}
	}
	releaseWriter, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release(&err, releaseWriter)
	ids, err := s.Inventory(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.Verify(ctx, id); err != nil {
			return err
		}
	}
	if _, err = s.VerifyRefs(ctx); err != nil {
		return err
	}
	// Republish even an identical retry to establish the durability barrier after
	// an uncertain earlier publication. No objects, refs, HEAD or bytes change.
	return s.writeFile(ctx, filepath.Join(s.meta, "format"), []byte(filesystemMarker))
}
