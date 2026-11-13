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
const stagingMarker = "pebble-format 2\nrequires staging-v1\n"
const stagingFilesystemMarker = "pebble-format 2\nrequires filesystem-v1\nrequires staging-v1\n"

func (s *Store) marker() (_ string, err error) {
	f, err := regular(filepath.Join(s.meta, "format"))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var data [len(stagingFilesystemMarker) + 1]byte
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
	if n == len(stagingMarker) && bytes.Equal(data[:n], []byte(stagingMarker)) {
		return stagingMarker, nil
	}
	if n == len(stagingFilesystemMarker) && bytes.Equal(data[:n], []byte(stagingFilesystemMarker)) {
		return stagingFilesystemMarker, nil
	}
	return "", ErrFormat
}

// FilesystemEnabled checks persistent capability metadata, never a cached Open.
func (s *Store) FilesystemEnabled() (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	marker, err := s.marker()
	return marker == filesystemMarker || marker == stagingFilesystemMarker, err
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
	return s.enableCapability(ctx, false)
}

// EnableStaging excludes whole-tree-only writers before publishing index facts.
func (s *Store) EnableStaging(ctx context.Context) error {
	return s.enableCapability(ctx, true)
}

func (s *Store) enableCapability(ctx context.Context, staging bool) (err error) {
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
	marker, err := s.marker()
	if err != nil {
		return err
	}
	hasStaging := marker == stagingMarker || marker == stagingFilesystemMarker
	hasFilesystem := marker == filesystemMarker || marker == stagingFilesystemMarker
	if staging && hasStaging {
		// Reaffirm the marker barrier, without treating an explicit add as fsck
		// of unrelated unreachable objects in an already-enabled repository.
		return s.writeFile(ctx, filepath.Join(s.meta, "format"), []byte(marker))
	}
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
	if staging && !hasStaging {
		// Only an empty index may predate interrupted capability publication.
		data, e := s.readIndexBytes()
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e == nil && !bytes.Equal(data, emptyIndex) {
			return fmt.Errorf("unexpected index before staging enablement")
		}
		if err = s.writeFile(ctx, filepath.Join(s.meta, "index"), emptyIndex); err != nil {
			return err
		}
	}
	hasStaging = hasStaging || staging
	hasFilesystem = hasFilesystem || !staging
	target := formatMarker
	if hasFilesystem {
		target = filesystemMarker
	}
	if hasStaging {
		target = stagingMarker
		if hasFilesystem {
			target = stagingFilesystemMarker
		}
	}
	return s.writeFile(ctx, filepath.Join(s.meta, "format"), []byte(target))
}
