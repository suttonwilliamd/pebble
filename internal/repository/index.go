package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// Identity contains explicit provenance, not authentication or inferred host data.
type Identity struct{ Author, Email, Committer, CommitterEmail string }

func (r *Repository) index() (*storev2.IndexState, []storev2.TreeEntry, error) {
	head, base, err := r.current()
	if err != nil {
		return nil, nil, err
	}
	state, err := r.store.ReadIndex(context.Background())
	if err != nil {
		return nil, nil, err
	}
	if state == nil {
		entries, e := r.tree(base)
		return nil, entries, e
	}
	if state.Head != head || base != state.Base && (state.Next == (storev2.ID{}) || base != state.Next) {
		return nil, nil, fmt.Errorf("stale staging index: HEAD changed; preserve index and history")
	}
	entries, err := r.store.ReadTree(context.Background(), state.Tree)
	if err == nil {
		err = validateEntries(entries)
	}
	if err != nil {
		return nil, nil, err
	}
	if state.Next != (storev2.ID{}) && base == state.Next {
		return nil, entries, nil
	} // Exact prepared commit consumed it.
	return state, entries, nil
}

func treeChanges(before, after []storev2.TreeEntry) []Change {
	old := make(map[string]storev2.TreeEntry, len(before))
	for _, entry := range before {
		old[entry.Path] = entry
	}
	changes := []Change{}
	for _, entry := range after {
		prior, ok := old[entry.Path]
		if !ok {
			changes = append(changes, Change{"A", entry.Path})
		} else if prior != entry {
			changes = append(changes, Change{"M", entry.Path})
		}
		delete(old, entry.Path)
	}
	for name := range old {
		changes = append(changes, Change{"D", name})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// Stage captures only selected files/directories, including selected deletions.
// Paths are literal repository-relative paths; "." selects the entire repository.
func (r *Repository) Stage(paths []string) (err error) {
	if len(paths) == 0 {
		return fmt.Errorf("add requires at least one path")
	}
	for _, path := range paths {
		if path != "." {
			if err = validPath(path); err != nil {
				return err
			}
		}
	}
	if err = r.store.EnableStaging(context.Background()); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	head, base, err := r.current()
	if err != nil {
		return err
	}
	_, entries, err := r.index()
	if err != nil {
		return err
	}
	files, err := r.scanSelected(entries, paths, false)
	if err != nil {
		return err
	}
	for _, path := range paths {
		matched := path == "."
		for _, entry := range entries {
			matched = matched || selectedPath(entry.Path, []string{path})
		}
		for _, file := range files {
			matched = matched || selectedPath(file.name, []string{path})
		}
		if !matched {
			return fmt.Errorf("path is ignored or does not match a file: %s", path)
		}
	}
	captured, err := r.ingestWorking(files)
	if err != nil {
		return err
	}
	result := make([]storev2.TreeEntry, 0, len(entries)+len(captured))
	ancestors := map[string]bool{}
	for _, file := range captured {
		for end := strings.LastIndexByte(file.Path, '/'); end >= 0; end = strings.LastIndexByte(file.Path[:end], '/') {
			ancestors[file.Path[:end]] = true
		}
	}
	for _, entry := range entries {
		if !selectedPath(entry.Path, paths) && !ancestors[entry.Path] {
			result = append(result, entry)
		}
	}
	result = append(result, captured...)
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	if err = validateEntries(result); err != nil {
		return err
	}
	tree, err := r.store.CreateTree(context.Background(), result)
	if err != nil {
		return err
	}
	if err = r.boundary(context.Background(), "index-publish", head); err != nil {
		return err
	}
	if err = r.verifyWorkingOwnership(files); err != nil {
		return err
	}
	return r.store.WriteIndex(context.Background(), &storev2.IndexState{Head: head, Base: base, Tree: tree})
}

// Status uses Git-style XY codes: X = HEAD/index, Y = index/worktree; ?? = untracked.
func (r *Repository) Status() (_ []Change, err error) {
	unlock, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	_, entries, err := r.index()
	if err != nil {
		return nil, err
	}
	head, err := r.TreeEntries()
	if err != nil {
		return nil, err
	}
	files, err := r.scan(entries)
	if err != nil {
		return nil, err
	}
	working, err := r.changes(entries, files)
	if err != nil {
		return nil, err
	}
	codes := map[string]string{}
	for _, change := range treeChanges(head, entries) {
		codes[change.Path] = change.Code + " "
	}
	for _, change := range working {
		code, ok := codes[change.Path]
		if !ok {
			code = "  "
		}
		if change.Code == "A" {
			if ok {
				codes[change.Path] = code
				codes[change.Path+"\x00"] = "??"
			} else {
				codes[change.Path] = "??"
			}
		} else {
			codes[change.Path] = code[:1] + change.Code
		}
	}
	out := make([]Change, 0, len(codes))
	names := make([]string, 0, len(codes))
	for name := range codes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, Change{codes[name], strings.TrimSuffix(name, "\x00")})
	}
	return out, nil
}

// Commit consumes the immutable index. --all refreshes only paths currently in
// the index (including staged additions), never unrelated untracked data.
func (r *Repository) Commit(message string, identity Identity, all bool) (_ storev2.ID, err error) {
	for _, value := range []string{message, identity.Author, identity.Email, identity.Committer, identity.CommitterEmail} {
		if strings.TrimSpace(value) == "" {
			return storev2.ID{}, fmt.Errorf("commit message and author/committer identity are required")
		}
	}
	ctx := context.Background()
	if all {
		if err = r.store.EnableStaging(ctx); err != nil {
			return storev2.ID{}, err
		}
	}
	unlock, err := r.lock()
	if err != nil {
		return storev2.ID{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	head, parent, err := r.current()
	if err != nil {
		return storev2.ID{}, err
	}
	staged, entries, err := r.index()
	if err != nil {
		return storev2.ID{}, err
	}
	old, err := r.tree(parent)
	if err != nil {
		return storev2.ID{}, err
	}
	var files []workingFile
	if all {
		files, err = r.scanSelected(entries, []string{"."}, true)
		if err != nil {
			return storev2.ID{}, err
		}
		entries, err = r.ingestWorking(files)
		if err != nil {
			return storev2.ID{}, err
		}
	}
	if len(treeChanges(old, entries)) == 0 && !(parent == (storev2.ID{}) && staged != nil) {
		return storev2.ID{}, fmt.Errorf("nothing staged to commit; use pebble add <path> (or explicit commit --all for tracked changes)")
	}
	tree, err := r.store.CreateTree(ctx, entries)
	if err != nil {
		return storev2.ID{}, err
	}
	now := time.Now()
	c := storev2.CommitData{Tree: tree, Author: identity.Author, Email: identity.Email, Committer: identity.Committer, CommitterEmail: identity.CommitterEmail, Seconds: now.Unix(), Nanoseconds: uint32(now.Nanosecond()), Offset: now.Format("-0700"), Message: message}
	var expected *storev2.ID
	if parent != (storev2.ID{}) {
		c.Parents = []storev2.ID{parent}
		expected = &parent
	}
	id, err := r.store.CreateCommit(ctx, c)
	if err != nil {
		return storev2.ID{}, err
	}
	state := &storev2.IndexState{Head: head, Base: parent, Tree: tree, Next: id}
	if err = r.boundary(ctx, "commit-publish", head); err != nil {
		return storev2.ID{}, err
	}
	if err = r.verifyWorkingOwnership(files); err != nil {
		return storev2.ID{}, err
	}
	if err = r.store.WriteIndex(ctx, state); err != nil {
		return storev2.ID{}, err
	}
	if err = r.boundary(ctx, "commit-prepared", head); err != nil {
		return storev2.ID{}, err
	}
	if err = r.store.UpdateRef(ctx, head, expected, id); err != nil {
		return storev2.ID{}, err
	}
	if err = r.boundary(ctx, "commit-published", head); err != nil {
		return storev2.ID{}, err
	}
	// A crash before this cleanup leaves Next as exact proof of consumption.
	if err = r.store.WriteIndex(ctx, nil); err != nil {
		return storev2.ID{}, err
	}
	return id, nil
}

func (r *Repository) cleanIndexForCheckout() error {
	_, entries, err := r.index()
	if err != nil {
		return err
	}
	head, err := r.TreeEntries()
	if err != nil {
		return err
	}
	if len(treeChanges(head, entries)) != 0 {
		return fmt.Errorf("checkout refused: staged changes; commit them first")
	}
	enabled, err := r.store.StagingEnabled()
	if err != nil {
		return err
	}
	if enabled {
		return r.store.WriteIndex(context.Background(), nil)
	}
	return nil
}
