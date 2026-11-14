package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func (r *Repository) stageFile(dir string, e storev2.TreeEntry) (string, error) {
	return r.stageFileContext(context.Background(), dir, e)
}
func (r *Repository) stageFileContext(ctx context.Context, dir string, e storev2.TreeEntry) (_ string, err error) {
	if e.Executable && !measuresExecutable {
		return "", fmt.Errorf("host cannot materialize executable identity: %s; history preserved", e.Path)
	}
	if e.Symlink {
		size, _, err := r.store.FileInfo(ctx, e.Manifest)
		if err != nil {
			return "", err
		}
		if size == 0 || size > maxLinkBytes {
			return "", fmt.Errorf("unsupported symbolic-link target length: %s", e.Path)
		}
		var target bytes.Buffer
		if err = r.store.CopyFile(ctx, e.Manifest, &target); err != nil {
			return "", err
		}
		if err = validateMaterializableLink(target.String()); err != nil {
			return "", err
		}
		f, err := os.CreateTemp(dir, "link-")
		if err != nil {
			return "", err
		}
		p := f.Name()
		if err = errors.Join(f.Close(), os.Remove(p)); err != nil {
			return "", err
		}
		if err = createLink(target.String(), p, e.DirectoryLink); err != nil {
			return "", fmt.Errorf("host cannot create symbolic link %s: %w", e.Path, err)
		}
		link, err := readLink(p)
		if err != nil || link.target != target.String() || measuresDirectoryLink && link.directory != e.DirectoryLink {
			os.Remove(p)
			return "", errors.Join(fmt.Errorf("host cannot preserve literal link target/type: %s", e.Path), err)
		}
		if err = r.boundary(ctx, "stage-sync", e.Path); err == nil {
			err = syncOriginal(p)
		}
		if err != nil {
			os.Remove(p)
			return "", err
		}
		return p, nil
	}
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
	// CopyFile can produce bytes before a late integrity failure. Only this private
	// stage sees them until verification, mode, file barrier and close all pass.
	err = r.store.CopyFile(ctx, e.Manifest, f)
	if err == nil {
		err = f.Chmod(mode(e.Executable))
	}
	if err == nil {
		err = r.boundary(ctx, "stage-sync", e.Path)
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
	return err == nil && f.size == length && f.sum == sum && (f.executable == e.Executable || !measuresExecutable && !f.symlink) && f.symlink == e.Symlink && (!measuresDirectoryLink || f.directoryLink == e.DirectoryLink), err
}

// Restore explicitly discards edits to one tracked file. After interruption,
// Recover finishes that same intent, but refuses any subsequent external edit.
func (r *Repository) Restore(name string) error { return r.RestoreContext(context.Background(), name) }
func (r *Repository) RestoreContext(ctx context.Context, name string) (err error) {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if _, _, err = r.index(); err != nil {
		return err
	}
	head, id, err := r.current()
	if err != nil {
		return err
	}
	entries, err := r.tree(id)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Path == name {
			return r.prepareWorktree(ctx, "restore", name, head, head, id, id, nil, []storev2.TreeEntry{entry})
		}
	}
	return fmt.Errorf("path is not tracked at HEAD: %s", name)
}

// Checkout refuses tracked edits and conflicting untracked paths. File,
// directory and link transitions publish only after a durable validated intent.
func (r *Repository) Checkout(name string) error {
	return r.CheckoutContext(context.Background(), name)
}
func (r *Repository) CheckoutContext(ctx context.Context, name string) (err error) {
	if !storev2.ValidBranch(name) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	head, oldID, err := r.current()
	if err != nil {
		return err
	}
	ref := "heads/" + name
	id, err := r.GetRef(ref)
	if err != nil {
		return err
	}
	old, err := r.tree(oldID)
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
	if err = r.cleanIndexForCheckout(); err != nil {
		return err
	}
	return r.prepareWorktree(ctx, "checkout", "", head, ref, oldID, id, old, target)
}

func (r *Repository) prepareWorktree(ctx context.Context, kind, name, beforeHead, afterHead string, beforeID, afterID storev2.ID, old, target []storev2.TreeEntry, selection ...restoreSelection) (err error) {
	for _, entry := range target {
		if entry.Executable && !measuresExecutable {
			return fmt.Errorf("host cannot materialize executable identity: %s; history preserved", entry.Path)
		}
	}
	dir, err := os.MkdirTemp(filepath.Join(r.root, ".pebble"), "worktree-stage-")
	if err != nil {
		return err
	}
	// Only the unpublished private name is removed here, never an active journal.
	defer os.RemoveAll(dir)
	if err = preflightFilesystemPaths(dir, target); err != nil {
		return err
	}
	plan := worktreeIntent{Kind: kind, RestorePath: name, BeforeHead: beforeHead, AfterHead: afterHead, BeforeID: beforeID, AfterID: afterID, RecoveryID: filepath.Base(dir), version: 3}
	if len(selection) != 0 {
		plan.version = 4
		plan.RestoreSource = &selection[0].Source
		plan.RestoreIndex = selection[0].Index
	}
	before, after := map[string]storev2.TreeEntry{}, map[string]storev2.TreeEntry{}
	names := map[string]bool{}
	for _, e := range old {
		before[e.Path] = e
		names[e.Path] = true
	}
	for _, e := range target {
		after[e.Path] = e
		names[e.Path] = true
	}
	sorted := make([]string, 0, len(names))
	for path := range names {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)
	for _, path := range sorted {
		plan.Files = append(plan.Files, journalFile{Path: path})
	}
	if err = r.prepareDirectories(dir, &plan, target); err != nil {
		return err
	}
	for i, path := range sorted {
		if err = r.boundary(ctx, "prepare-file", path); err != nil {
			return err
		}
		p := filepath.Join(r.root, filepath.FromSlash(path))
		original, err := r.inspectLiveImage(&plan, path)
		if err != nil {
			return err
		}
		file := journalFile{Path: path, Original: original}
		if entry, tracked := before[path]; tracked && kind == "checkout" {
			ok, err := r.matches(entry)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("file changed during checkout: %s", path)
			}
		}
		if entry, exists := after[path]; exists {
			_, tracked := before[path]
			if original != nil && (kind == "checkout" || plan.version == 4 && !tracked) {
				ok, err := r.matches(entry)
				if err != nil {
					return err
				}
				if _, tracked := before[path]; !tracked && !ok {
					return fmt.Errorf("checkout would overwrite untracked file: %s", path)
				}
				file.Keep = ok
			}
			if file.Keep {
				file.Target = original
			} else {
				temporary, err := r.stageFileContext(ctx, dir, entry)
				if err != nil {
					return err
				}
				if err = os.Rename(temporary, filepath.Join(dir, stageName(i))); err != nil {
					return err
				}
				file.Target, err = inspectImage(&Repository{root: dir}, stageName(i))
				if err != nil {
					return err
				}
			}
		}
		// Original bytes themselves are atomically moved into the retained slot.
		// Flush them before intent: restore's dirty original may not be in the store.
		if original != nil {
			if err = syncOriginal(p); err != nil {
				return err
			}
		}
		if original != nil && !file.Keep {
			backup, err := r.copyImage(ctx, path, dir, original)
			if err != nil {
				return err
			}
			if err = os.Rename(backup, filepath.Join(dir, preimageName(i))); err != nil {
				return err
			}
			file.Backup, err = inspectImage(&Repository{root: dir}, preimageName(i))
			if err != nil {
				return err
			}
		}
		plan.Files[i] = file
	}
	if err = r.validateIntent(&plan); err != nil {
		return err
	}
	if err = r.inspectTransaction(dir, &plan); err != nil {
		return err
	}
	if err = r.preflightPermissions(dir, &plan); err != nil {
		return err
	}
	if err = r.inspectTransaction(dir, &plan); err != nil {
		return err
	}
	if err = writeIntent(dir, &plan); err != nil {
		return err
	}
	if err = r.boundary(ctx, "journal-publish", dir); err != nil {
		return err
	}
	if err = publishMigration(dir, r.journalPath()); err != nil {
		return fmt.Errorf("journal publication uncertain; run pebble recover: %w", err)
	}
	if err = r.boundary(ctx, "journal-published", r.journalPath()); err != nil {
		return err
	}
	return r.resumeWorktree(ctx, &plan)
}
