package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Change struct{ Code, Path string }

// TreeEntries returns the current committed files. HEAD, not the index cache, is authoritative.
func (r *Repository) TreeEntries() ([]TreeEntry, error) {
	ref, err := r.GetHead()
	if err != nil {
		return nil, err
	}
	hash, err := r.GetRef(ref)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		return []TreeEntry{}, nil
	}
	return r.treeForCommit(hash)
}
func (r *Repository) treeForCommit(hash string) ([]TreeEntry, error) {
	c, err := r.GetCommit(hash)
	if err != nil {
		return nil, err
	}
	obj, err := r.GetObject(c.Tree)
	if err != nil {
		return nil, err
	}
	if obj.Type != ObjectTypeTree {
		return nil, fmt.Errorf("expected tree %s", c.Tree)
	}
	var entries []TreeEntry
	if err = json.Unmarshal(obj.Content, &entries); err != nil {
		return nil, err
	}
	if err = validateEntries(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func validateEntries(entries []TreeEntry) error {
	seen := map[string]bool{}
	for _, e := range entries {
		if err := validFilePath(e.Name); err != nil {
			return err
		}
		key := strings.ToLower(e.Name)
		if seen[key] {
			return fmt.Errorf("duplicate or case-colliding path: %s", e.Name)
		}
		seen[key] = true
		if e.Type != ObjectTypeBlob || !validHash(e.Hash) {
			return fmt.Errorf("invalid tree entry %s", e.Name)
		}
	}
	for name := range seen {
		for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if seen[parent] {
				return fmt.Errorf("file/directory collision at %s", parent)
			}
		}
	}
	return nil
}
func validFilePath(name string) error {
	normalized := filepath.ToSlash(name)
	if normalized == "" || strings.Contains(name, "\\") || strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("unsafe snapshot path %q", name)
	}
	for _, part := range strings.Split(normalized, "/") {
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, ":<>\"|?*\x00\r\n") || reserved {
			return fmt.Errorf("unsafe or nonportable snapshot path %q", name)
		}
	}
	return nil
}

// safePath rejects symlink ancestors, including when reading an already tracked file.
func (r *Repository) safePath(name string) (string, error) {
	if err := validFilePath(name); err != nil {
		return "", err
	}
	p := r.rootPath
	parts := strings.Split(name, "/")
	for i, part := range parts {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink in snapshot path %s", name)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("file blocks directory in %s", name)
		}
	}
	return p, nil
}
func (r *Repository) workingIndex() (*Index, error) {
	patterns, err := ReadIgnore(r.rootPath)
	if err != nil {
		return nil, err
	}
	idx, err := NewFileIndexGenerator(r.rootPath, patterns).GenerateIndex()
	if err != nil {
		return nil, err
	}
	// Ignore rules only exclude untracked files, never silently delete committed files.
	old, err := r.TreeEntries()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range idx.Entries {
		seen[e.Path] = true
	}
	for _, e := range old {
		if seen[e.Name] {
			continue
		}
		p, err := r.safePath(e.Name)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("tracked file replaced by non-file: %s", e.Name)
		}
		obj, err := CreateBlob(p)
		if err != nil {
			return nil, err
		}
		idx.Entries = append(idx.Entries, IndexEntry{Path: e.Name, Mode: info.Mode().String(), Size: obj.Size, Mtime: info.ModTime(), Hash: obj.Hash})
	}
	sort.Slice(idx.Entries, func(i, j int) bool { return idx.Entries[i].Path < idx.Entries[j].Path })
	return idx, nil
}
func executable(mode string) bool { return strings.Contains(mode, "x") }
func (r *Repository) Status() ([]Change, error) {
	idx, err := r.workingIndex()
	if err != nil {
		return nil, err
	}
	old, err := r.TreeEntries()
	if err != nil {
		return nil, err
	}
	before := map[string]TreeEntry{}
	for _, e := range old {
		before[e.Name] = e
	}
	changes := []Change{}
	for _, e := range idx.Entries {
		prev, ok := before[e.Path]
		if !ok {
			changes = append(changes, Change{"A", e.Path})
		} else if prev.Hash != e.Hash || executable(prev.Mode) != executable(e.Mode) {
			changes = append(changes, Change{"M", e.Path})
		}
		delete(before, e.Path)
	}
	for name := range before {
		changes = append(changes, Change{"D", name})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}
func (r *Repository) Commit(message, author, email string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("commit message cannot be empty")
	}
	unlock, err := r.Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	idx, err := r.workingIndex()
	if err != nil {
		return "", err
	}
	entries := []TreeEntry{}
	for _, e := range idx.Entries {
		p, err := r.safePath(e.Path)
		if err != nil {
			return "", err
		}
		obj, err := CreateBlob(p)
		if err != nil {
			return "", err
		}
		if obj.Hash != e.Hash {
			return "", fmt.Errorf("file changed while committing: %s; retry", e.Path)
		}
		if err = r.StoreObject(obj); err != nil {
			return "", err
		}
		entries = append(entries, TreeEntry{Name: e.Path, Mode: e.Mode, Hash: obj.Hash, Type: ObjectTypeBlob})
	}
	if err = validateEntries(entries); err != nil {
		return "", err
	}
	tree := CreateTree(entries)
	ref, err := r.GetHead()
	if err != nil {
		return "", err
	}
	parent, err := r.GetRef(ref)
	if err != nil {
		return "", err
	}
	if parent != "" {
		c, err := r.GetCommit(parent)
		if err != nil {
			return "", err
		}
		if c.Tree == tree.Hash {
			return "", fmt.Errorf("nothing to commit; working tree clean")
		}
	}
	if err = r.StoreObject(tree); err != nil {
		return "", err
	}
	commit := CreateCommit(tree.Hash, parent, author, email, message)
	if err = r.StoreObject(commit); err != nil {
		return "", err
	}
	// The ref is the commit point; no fallible operations follow it.
	if err = r.SetRef(ref, commit.Hash); err != nil {
		return "", err
	}
	return commit.Hash, nil
}
func (r *Repository) CreateBranch(name string) error {
	ref := "heads/" + name
	if err := validateRef(ref); err != nil {
		return err
	}
	unlock, err := r.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err = os.Stat(filepath.Join(r.refsPath, filepath.FromSlash(ref))); err == nil {
		return fmt.Errorf("branch already exists: %s", name)
	} else if !os.IsNotExist(err) {
		return err
	}
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	hash, err := r.GetRef(head)
	if err != nil {
		return err
	}
	return r.SetRef(ref, hash)
}

// Checkout preflights every target object and refuses dirty tracked files or untracked collisions.
// File/directory transitions are deliberately rejected in this first implementation.
func (r *Repository) Checkout(name string) error {
	ref := "heads/" + name
	if err := validateRef(ref); err != nil {
		return err
	}
	unlock, err := r.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	hash, err := r.GetRef(ref)
	if err != nil {
		return err
	}
	changes, err := r.Status()
	if err != nil {
		return err
	}
	for _, c := range changes {
		if c.Code != "A" {
			return fmt.Errorf("checkout refused: uncommitted change to %s", c.Path)
		}
	}
	old, err := r.TreeEntries()
	if err != nil {
		return err
	}
	target := []TreeEntry{}
	if hash != "" {
		target, err = r.treeForCommit(hash)
		if err != nil {
			return err
		}
	}
	oldMap := map[string]TreeEntry{}
	backup := map[string][]byte{}
	data := map[string][]byte{}
	adopt := map[string]bool{} // Identical untracked files need not be overwritten.
	for _, e := range old {
		oldMap[e.Name] = e
		p, err := r.safePath(e.Name)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if ComputeHash(b) != e.Hash {
			return fmt.Errorf("file changed during checkout: %s", e.Name)
		}
		backup[e.Name] = b
	}
	for _, e := range target {
		p, err := r.safePath(e.Name)
		if err != nil {
			return err
		}
		if info, statErr := os.Lstat(p); statErr == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("checkout would overwrite directory: %s", e.Name)
			}
			if _, tracked := oldMap[e.Name]; !tracked {
				existing, err := CreateBlob(p)
				if err != nil {
					return err
				}
				if existing.Hash != e.Hash || executable(info.Mode().String()) != executable(e.Mode) {
					return fmt.Errorf("checkout would overwrite untracked file: %s", e.Name)
				}
				adopt[e.Name] = true
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		obj, err := r.GetObject(e.Hash)
		if err != nil {
			return err
		}
		if obj.Type != ObjectTypeBlob {
			return fmt.Errorf("not a blob: %s", e.Hash)
		}
		data[e.Name] = obj.Content
	}
	// An ordinary I/O failure rolls back file content. Process termination/power loss is not transactional.
	written := []string{}
	rollback := func(cause error) error {
		var failures []string
		for _, name := range written {
			if err := os.Remove(filepath.Join(r.rootPath, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
				failures = append(failures, err.Error())
			}
		}
		for _, e := range old {
			p := filepath.Join(r.rootPath, filepath.FromSlash(e.Name))
			if err := atomicWrite(p, backup[e.Name], fileMode(e.Mode)); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("%w; rollback incomplete: %s (committed objects remain intact)", cause, strings.Join(failures, "; "))
		}
		return cause
	}
	for _, e := range old {
		if err = os.Remove(filepath.Join(r.rootPath, filepath.FromSlash(e.Name))); err != nil {
			return rollback(err)
		}
	}
	for _, e := range target {
		if adopt[e.Name] {
			continue
		}
		p := filepath.Join(r.rootPath, filepath.FromSlash(e.Name))
		if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return rollback(err)
		}
		if err = atomicWrite(p, data[e.Name], fileMode(e.Mode)); err != nil {
			return rollback(err)
		}
		written = append(written, e.Name)
	}
	if err = r.SetHead(ref); err != nil {
		return rollback(err)
	}
	return nil
}
func fileMode(mode string) os.FileMode {
	if executable(mode) {
		return 0755
	}
	return 0644
}

// Verify checks the entire object graph reachable from every branch.
func (r *Repository) Verify() (int, error) {
	head, err := r.GetHead()
	if err != nil {
		return 0, err
	}
	if _, err = r.GetRef(head); err != nil {
		return 0, err
	}
	refs, err := r.GetRefs()
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	var visit func(string, ObjectType) error
	visit = func(hash string, kind ObjectType) error {
		obj, err := r.GetObject(hash)
		if err != nil {
			return err
		}
		if obj.Type != kind {
			return fmt.Errorf("unexpected type for %s", hash)
		}
		if seen[hash] {
			return nil
		}
		seen[hash] = true
		switch kind {
		case ObjectTypeCommit:
			c, err := r.GetCommit(hash)
			if err != nil {
				return err
			}
			if err = visit(c.Tree, ObjectTypeTree); err != nil {
				return err
			}
			if c.Parent != "" {
				return visit(c.Parent, ObjectTypeCommit)
			}
		case ObjectTypeTree:
			var entries []TreeEntry
			if err = json.Unmarshal(obj.Content, &entries); err != nil {
				return err
			}
			if err = validateEntries(entries); err != nil {
				return err
			}
			for _, e := range entries {
				if err = visit(e.Hash, ObjectTypeBlob); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, ref := range refs {
		if ref.Hash != "" {
			if err = visit(ref.Hash, ObjectTypeCommit); err != nil {
				return len(seen), fmt.Errorf("%s: %w", ref.Name, err)
			}
		}
	}
	return len(seen), nil
}
