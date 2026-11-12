package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

type workingFile struct {
	name       string
	executable bool
	size       uint64
	sum        [32]byte
}

func (r *Repository) readFile(name string, consume func(*os.File) error) (err error) {
	p, err := r.safePath(name)
	if err != nil {
		return err
	}
	before, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", name)
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) {
		return fmt.Errorf("file changed while opening: %s", name)
	}
	if err = consume(f); err != nil {
		return err
	}
	after, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.ModTime() != after.ModTime() || opened.Mode() != after.Mode() {
		return fmt.Errorf("file changed while reading: %s", name)
	}
	return nil
}
func (r *Repository) hashFile(name string) (workingFile, error) {
	result := workingFile{name: name}
	err := r.readFile(name, func(f *os.File) error {
		info, err := f.Stat()
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		if n != info.Size() {
			return fmt.Errorf("file changed while hashing: %s", name)
		}
		result.size = uint64(n)
		result.executable = info.Mode()&0111 != 0
		h.Sum(result.sum[:0])
		return nil
	})
	return result, err
}
func (r *Repository) scan(old []storev2.TreeEntry) ([]workingFile, error) {
	names := map[string]bool{}
	if err := snapshot.WalkFiles(r.root, func(name string, _ os.FileInfo) error {
		if err := validPath(name); err != nil {
			return err
		}
		names[name] = true
		return nil
	}); err != nil {
		return nil, err
	}
	for _, e := range old {
		p, err := r.safePath(e.Path)
		if err != nil {
			return nil, err
		}
		if _, err = os.Lstat(p); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		names[e.Path] = true
	}
	out := make([]workingFile, 0, len(names))
	entries := make([]storev2.TreeEntry, 0, len(names))
	for name := range names {
		f, err := r.hashFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		entries = append(entries, storev2.TreeEntry{Path: name})
	}
	if err := validateEntries(entries); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
func (r *Repository) changes(old []storev2.TreeEntry, files []workingFile) ([]Change, error) {
	before := map[string]storev2.TreeEntry{}
	for _, e := range old {
		before[e.Path] = e
	}
	changes := []Change{}
	for _, f := range files {
		e, ok := before[f.name]
		if !ok {
			changes = append(changes, Change{"A", f.name})
		} else {
			size, sum, err := r.store.FileInfo(context.Background(), e.Manifest)
			if err != nil {
				return nil, err
			}
			if size != f.size || sum != f.sum || e.Executable != f.executable {
				changes = append(changes, Change{"M", f.name})
			}
		}
		delete(before, f.name)
	}
	for name := range before {
		changes = append(changes, Change{"D", name})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}
func (r *Repository) Status() ([]Change, error) {
	old, err := r.TreeEntries()
	if err != nil {
		return nil, err
	}
	files, err := r.scan(old)
	if err != nil {
		return nil, err
	}
	return r.changes(old, files)
}
func (r *Repository) Commit(message, author, email string) (_ storev2.ID, err error) {
	if strings.TrimSpace(message) == "" || strings.TrimSpace(author) == "" || strings.TrimSpace(email) == "" {
		return storev2.ID{}, fmt.Errorf("commit message and identity are required")
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
	old, err := r.tree(parent)
	if err != nil {
		return storev2.ID{}, err
	}
	files, err := r.scan(old)
	if err != nil {
		return storev2.ID{}, err
	}
	changes, err := r.changes(old, files)
	if err != nil {
		return storev2.ID{}, err
	}
	if len(changes) == 0 && parent != (storev2.ID{}) {
		return storev2.ID{}, fmt.Errorf("nothing to commit; working tree clean")
	}
	ctx := context.Background()
	entries := make([]storev2.TreeEntry, 0, len(files))
	for _, file := range files {
		var id storev2.ID
		err = r.readFile(file.name, func(f *os.File) error {
			info, e := f.Stat()
			if e != nil {
				return e
			}
			if (info.Mode()&0111 != 0) != file.executable {
				return fmt.Errorf("mode changed while committing: %s", file.name)
			}
			id, e = r.store.IngestFile(ctx, f, nil)
			return e
		})
		if err != nil {
			return storev2.ID{}, err
		}
		size, sum, e := r.store.FileInfo(ctx, id)
		if e != nil {
			return storev2.ID{}, e
		}
		if size != file.size || sum != file.sum {
			return storev2.ID{}, fmt.Errorf("file changed while committing: %s", file.name)
		}
		entries = append(entries, storev2.TreeEntry{Path: file.name, Executable: file.executable, Manifest: id})
	}
	tree, err := r.store.CreateTree(ctx, entries)
	if err != nil {
		return storev2.ID{}, err
	}
	now := time.Now()
	c := storev2.CommitData{Tree: tree, Author: author, Email: email, Seconds: now.Unix(), Nanoseconds: uint32(now.Nanosecond()), Offset: now.Format("-0700"), Message: message}
	var expected *storev2.ID
	if parent != (storev2.ID{}) {
		c.Parents = []storev2.ID{parent}
		expected = &parent
	}
	id, err := r.store.CreateCommit(ctx, c)
	if err != nil {
		return storev2.ID{}, err
	}
	if err = r.store.UpdateRef(ctx, head, expected, id); err != nil {
		return storev2.ID{}, err
	}
	return id, nil
}
func (r *Repository) CreateBranch(name string) (err error) {
	if !storev2.ValidBranch(name) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	_, id, err := r.current()
	if err != nil {
		return err
	}
	if id == (storev2.ID{}) {
		return fmt.Errorf("cannot branch before the first commit")
	}
	refs, err := r.store.ListRefs(context.Background())
	if err != nil {
		return err
	}
	if _, ok := refs["heads/"+name]; ok {
		return fmt.Errorf("branch already exists: %s", name)
	}
	return r.store.UpdateRef(context.Background(), "heads/"+name, nil, id)
}

// Log visits all ordered parents, first-parent first, emitting each commit once.
// Ref opening has already validated closure and cycles; this adds traversal bounds.
func (r *Repository) Log(visit func(storev2.ID, storev2.CommitData) error) error {
	_, id, err := r.current()
	if err != nil {
		return err
	}
	if id == (storev2.ID{}) {
		return nil
	}
	pending := []storev2.ID{id}
	seen := map[storev2.ID]bool{}
	for len(pending) > 0 {
		id = pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[id] {
			continue
		}
		if len(seen) >= storev2.MaxGraphObjects {
			return storev2.ErrGraphLimit
		}
		seen[id] = true
		c, err := r.GetCommit(id)
		if err != nil {
			return err
		}
		if err = visit(id, c); err != nil {
			return err
		}
		for i := len(c.Parents) - 1; i >= 0; i-- {
			pending = append(pending, c.Parents[i])
		}
	}
	return nil
}
