// Package repository implements the local format-2 working-tree engine.
// Storage addresses, graph validation and ref publication belong to storev2.
package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/processlock"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

type Repository struct {
	root  string
	store *storev2.Store
	// Private, instance-scoped fault seam at real transaction I/O boundaries.
	fault func(stage, path string) error
}
type Change struct{ Code, Path string }
type Ref struct {
	Name string
	ID   storev2.ID
}

func Init(path string) (*Repository, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s, err := storev2.InitRepository(context.Background(), root)
	if err != nil {
		return nil, err
	}
	return &Repository{root: root, store: s}, nil
}
func Open(path string) (*Repository, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for {
		_, err = os.Lstat(filepath.Join(root, ".pebble"))
		if err == nil {
			s, err := storev2.Open(root)
			if err != nil {
				return nil, fmt.Errorf("cannot open repository (legacy/unmarked repositories require explicit migration): %w", err)
			}
			r := &Repository{root: root, store: s}
			if _, err = r.GetHead(); err != nil {
				return nil, err
			}
			return r, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, fmt.Errorf("not a Pebble repository (run pebble init)")
		}
		root = parent
	}
}
func (r *Repository) RootPath() string         { return r.root }
func (r *Repository) GetHead() (string, error) { return r.store.Head(context.Background()) }
func (r *Repository) GetRef(name string) (storev2.ID, error) {
	return r.store.GetRef(context.Background(), name)
}
func (r *Repository) GetCommit(id storev2.ID) (storev2.CommitData, error) {
	return r.store.ReadCommit(context.Background(), id)
}
func (r *Repository) GetRefs() ([]Ref, error) {
	refs, err := r.store.ListRefs(context.Background())
	if err != nil {
		return nil, err
	}
	out := []Ref{}
	for name, id := range refs {
		if strings.HasPrefix(name, "heads/") {
			out = append(out, Ref{name, id})
		}
	}
	if len(refs) == 0 {
		head, _, err := r.current()
		if err != nil {
			return nil, err
		}
		out = append(out, Ref{Name: head}) // Actual symbolic, unborn branch.
	}
	sortRefs(out)
	return out, nil
}
func (r *Repository) current() (string, storev2.ID, error) {
	head, err := r.GetHead()
	if err != nil {
		return "", storev2.ID{}, err
	}
	id, err := r.GetRef(head)
	if errors.Is(err, os.ErrNotExist) {
		refs, e := r.store.ListRefs(context.Background())
		if e != nil {
			return "", storev2.ID{}, e
		}
		if head == "heads/main" && len(refs) == 0 {
			return head, storev2.ID{}, nil
		}
	}
	return head, id, err
}
func (r *Repository) CurrentID() (storev2.ID, error) { _, id, err := r.current(); return id, err }
func (r *Repository) tree(id storev2.ID) ([]storev2.TreeEntry, error) {
	if id == (storev2.ID{}) {
		return []storev2.TreeEntry{}, nil
	}
	c, err := r.GetCommit(id)
	if err != nil {
		return nil, err
	}
	entries, err := r.store.ReadTree(context.Background(), c.Tree)
	if err != nil {
		return nil, err
	}
	if err = validateEntries(entries); err != nil {
		return nil, err
	}
	return entries, nil
}
func (r *Repository) TreeEntries() ([]storev2.TreeEntry, error) {
	_, id, err := r.current()
	if err != nil {
		return nil, err
	}
	return r.tree(id)
}
func (r *Repository) operationLock() (func() error, error) {
	if _, err := storev2.Open(r.root); err != nil {
		return nil, err
	}
	p := filepath.Join(r.root, ".pebble", "worktree.lock")
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("legacy working tree lock (inspect %s): %w", p, errors.Join(os.ErrExist, err))
	}
	unlock, err := processlock.Acquire(filepath.Join(r.root, ".pebble", "worktree.guard"))
	if err != nil {
		return nil, fmt.Errorf("working tree locked: %w", err)
	}
	return unlock, nil
}
func (r *Repository) lock() (func() error, error) {
	unlock, err := r.operationLock()
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(r.journalPath()); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(fmt.Errorf("working tree recovery required; run pebble recover"), err, unlock())
	}
	return unlock, nil
}
func (r *Repository) Verify() (int, error) {
	ctx := context.Background()
	ids, err := r.store.Inventory(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := r.store.Verify(ctx, id); err != nil {
			return 0, err
		}
	}
	if _, _, err := r.current(); err != nil {
		return 0, err
	}
	if _, err := r.store.VerifyRefs(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}
