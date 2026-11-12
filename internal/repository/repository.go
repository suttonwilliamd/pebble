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

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

type Repository struct {
	root  string
	store *storev2.Store
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
	return &Repository{root, s}, nil
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
			r := &Repository{root, s}
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
func (r *Repository) lock() (func() error, error) {
	// Cooperating CLI operations hold this lock across working-tree preflight and
	// HEAD/ref publication. Store writes independently acquire store's write lock.
	if _, err := storev2.Open(r.root); err != nil {
		return nil, err
	}
	p := filepath.Join(r.root, ".pebble", "worktree.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("working tree locked (inspect %s): %w", p, err)
	}
	if err = f.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(p))
	}
	return func() error { return os.Remove(p) }, nil
}
func (r *Repository) Verify() (int, error) {
	if _, _, err := r.current(); err != nil {
		return 0, err
	}
	return r.store.VerifyRefs(context.Background())
}
