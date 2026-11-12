package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func (r *Repository) stageFile(dir string, e storev2.TreeEntry) (_ string, err error) {
	f, err := os.CreateTemp(dir, "file-")
	if err != nil {
		return "", err
	}
	p := f.Name()
	defer func() {
		if err != nil {
			os.Remove(p)
		}
	}()
	// CopyFile may write bytes before a late integrity failure. Nothing outside
	// this private stage can observe them until verification, sync and close pass.
	err = r.store.CopyFile(context.Background(), e.Manifest, f)
	if err == nil {
		err = f.Chmod(mode(e.Executable))
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", err
	}
	return p, nil
}
func (r *Repository) matches(e storev2.TreeEntry) (bool, error) {
	f, err := r.hashFile(e.Path)
	if err != nil {
		return false, err
	}
	length, sum, err := r.store.FileInfo(context.Background(), e.Manifest)
	return err == nil && f.size == length && f.sum == sum && f.executable == e.Executable, err
}
func (r *Repository) Restore(name string) (err error) {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	entries, err := r.TreeEntries()
	if err != nil {
		return err
	}
	var entry *storev2.TreeEntry
	for i := range entries {
		if entries[i].Path == name {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("path is not tracked at HEAD: %s", name)
	}
	target, err := r.safePath(name)
	if err != nil {
		return err
	}
	if info, e := os.Lstat(target); e == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("restore would overwrite non-file: %s", name)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	dir, err := os.MkdirTemp(filepath.Join(r.root, ".pebble"), "worktree-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	staged, err := r.stageFile(dir, *entry)
	if err != nil {
		return err
	}
	if _, err = r.safePath(name); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return os.Rename(staged, target)
}

// Checkout preflights all data before changing any user path. Ordinary I/O
// failures roll back from verified on-disk backups. This is not a crash journal.
// File/directory transitions remain explicitly unsupported, never skipped.
func (r *Repository) Checkout(name string) (err error) {
	if !storev2.ValidBranch(name) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	ctx := context.Background()
	head, _, err := r.current()
	if err != nil {
		return err
	}
	ref := "heads/" + name
	id, err := r.GetRef(ref)
	if err != nil {
		return err
	}
	old, err := r.TreeEntries()
	if err != nil {
		return err
	}
	target, err := r.tree(id)
	if err != nil {
		return err
	}
	files, err := r.scan(old)
	if err != nil {
		return err
	}
	changes, err := r.changes(old, files)
	if err != nil {
		return err
	}
	for _, c := range changes {
		if c.Code != "A" {
			return fmt.Errorf("checkout refused: uncommitted change to %s", c.Path)
		}
	}
	dir, err := os.MkdirTemp(filepath.Join(r.root, ".pebble"), "worktree-")
	if err != nil {
		return err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			os.RemoveAll(dir)
		}
	}()
	if err = preflightFilesystemPaths(dir, target); err != nil {
		return err
	}
	before := map[string]storev2.TreeEntry{}
	backup := map[string]string{}
	staged := map[string]string{}
	adopt := map[string]bool{}
	for _, e := range old {
		before[e.Path] = e
		backup[e.Path], err = r.stageFile(dir, e)
		if err != nil {
			return err
		}
	}
	preflight := func() error {
		for _, e := range old {
			ok, err := r.matches(e)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("file changed during checkout: %s", e.Path)
			}
		}
		for _, e := range target {
			p, err := r.safePath(e.Path)
			if err != nil {
				return err
			}
			info, err := os.Lstat(p)
			if err == nil {
				if !info.Mode().IsRegular() {
					return fmt.Errorf("checkout would overwrite directory or non-file: %s", e.Path)
				}
				if _, tracked := before[e.Path]; !tracked {
					ok, err := r.matches(e)
					if err != nil {
						return err
					}
					if !ok {
						return fmt.Errorf("checkout would overwrite untracked file: %s", e.Path)
					}
					adopt[e.Path] = true
				}
			} else if !os.IsNotExist(err) {
				return err
			} else {
				delete(adopt, e.Path)
			}
		}
		return nil
	}
	if err = preflight(); err != nil {
		return err
	}
	for _, e := range target {
		staged[e.Path], err = r.stageFile(dir, e)
		if err != nil {
			return err
		}
	}
	if err = preflight(); err != nil {
		return err
	}
	written := []string{}
	removed := []string{}
	rollback := func(cause error) error {
		var failures error
		for _, name := range written {
			if e := os.Remove(filepath.Join(r.root, filepath.FromSlash(name))); e != nil && !os.IsNotExist(e) {
				failures = errors.Join(failures, e)
			}
		}
		for _, name := range removed {
			if e := os.Rename(backup[name], filepath.Join(r.root, filepath.FromSlash(name))); e != nil {
				failures = errors.Join(failures, e)
			}
		}
		if failures != nil {
			keepStage = true
			return errors.Join(cause, fmt.Errorf("rollback incomplete; backups retained at %s; committed objects remain intact: %w", dir, failures))
		}
		return cause
	}
	for _, e := range old {
		p, err := r.safePath(e.Path)
		if err != nil {
			return rollback(err)
		}
		if err = os.Remove(p); err != nil {
			return rollback(err)
		}
		removed = append(removed, e.Path)
	}
	for _, e := range target {
		if adopt[e.Path] {
			continue
		}
		p, err := r.safePath(e.Path)
		if err != nil {
			return rollback(err)
		}
		if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return rollback(err)
		}
		if err = os.Rename(staged[e.Path], p); err != nil {
			return rollback(err)
		}
		written = append(written, e.Path)
	}
	if err = r.store.UpdateHead(ctx, head, ref); err != nil {
		observed, readErr := r.GetHead()
		if readErr != nil || observed != head || errors.Is(err, storev2.ErrPublicationUncertain) {
			keepStage = true
			return errors.Join(err, readErr, fmt.Errorf("HEAD publication may have completed; inspect repository before retry"))
		}
		return rollback(err)
	}
	return nil
}
