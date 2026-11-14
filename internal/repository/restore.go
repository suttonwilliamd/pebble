package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

type restoreSelection struct {
	Source storev2.ID
	Index  *storev2.ID
}

// RestoreFromContext explicitly discards edits only to selected HEAD/index
// tracked paths. Source-only paths are installed without clobbering untracked
// data. A literal file/subtree (or .) is selected; HEAD and index never move.
func (r *Repository) RestoreFromContext(ctx context.Context, revision, name string) (err error) {
	if name != "." {
		if err = validPath(name); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	state, _, err := r.index()
	if err != nil {
		return err
	}
	head, id, err := r.current()
	if err != nil {
		return err
	}
	source, err := r.ResolveRevision(ctx, revision)
	if err != nil {
		return err
	}
	selection := restoreSelection{Source: source}
	if state != nil {
		tree := state.Tree
		selection.Index = &tree
	}
	plan := worktreeIntent{Kind: "restore", RestorePath: name, BeforeID: id, AfterID: id, RestoreSource: &selection.Source, RestoreIndex: selection.Index, version: 4}
	old, target, err := r.restoreSelection(&plan)
	if err != nil {
		return err
	}
	return r.prepareWorktree(ctx, "restore", name, head, head, id, id, old, target, selection)
}

// The exact source and staging-tree links are immutable replay evidence. No
// replay consults a later index or resolves a branch name again.
func (r *Repository) restoreSelection(plan *worktreeIntent) ([]storev2.TreeEntry, []storev2.TreeEntry, error) {
	if plan.RestoreSource == nil || plan.RestoreSource.Kind != storev2.Commit {
		return nil, nil, fmt.Errorf("restore source must be a commit")
	}
	if plan.RestorePath != "." {
		if err := validPath(plan.RestorePath); err != nil {
			return nil, nil, err
		}
	}
	before, err := r.tree(plan.BeforeID)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]storev2.TreeEntry{}
	for _, entry := range before {
		if selectedPath(entry.Path, []string{plan.RestorePath}) {
			known[entry.Path] = entry
		}
	}
	if plan.RestoreIndex != nil {
		if err = r.store.Verify(context.Background(), *plan.RestoreIndex); err != nil {
			return nil, nil, err
		}
		entries, e := r.store.ReadTree(context.Background(), *plan.RestoreIndex)
		if e != nil {
			return nil, nil, e
		}
		if e = validateEntries(entries); e != nil {
			return nil, nil, e
		}
		for _, entry := range entries {
			if selectedPath(entry.Path, []string{plan.RestorePath}) {
				known[entry.Path] = entry
			}
		}
	}
	source, err := r.tree(*plan.RestoreSource)
	if err != nil {
		return nil, nil, err
	}
	target := []storev2.TreeEntry{}
	for _, entry := range source {
		if selectedPath(entry.Path, []string{plan.RestorePath}) {
			target = append(target, entry)
		}
	}
	if len(known) == 0 && len(target) == 0 {
		return nil, nil, fmt.Errorf("path is absent from source, HEAD and index: %s", plan.RestorePath)
	}
	old := make([]storev2.TreeEntry, 0, len(known))
	for _, entry := range known {
		old = append(old, entry)
	}
	sort.Slice(old, func(i, j int) bool { return old[i].Path < old[j].Path })
	return old, target, nil
}
