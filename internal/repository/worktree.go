package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

type workingFile struct {
	name                   string
	executable             bool
	symlink, directoryLink bool
	size                   uint64
	sum                    [32]byte
	info                   os.FileInfo
}

func (r *Repository) readFile(name string, consume func(*os.File) error) error {
	_, err := r.readFileWithPresence(name, consume)
	return err
}

// Presence distinguishes an absent optional rule file from one removed during
// opening/reading, without changing the original error shape used by recovery.
func (r *Repository) readFileWithPresence(name string, consume func(*os.File) error) (_ bool, err error) {
	p, err := r.safePath(name)
	if err != nil {
		return false, err
	}
	before, err := os.Lstat(p)
	if err != nil {
		return false, err
	}
	if !before.Mode().IsRegular() {
		return true, fmt.Errorf("not a regular file: %s", name)
	}
	// Windows Lstat resolves identity lazily by pathname. Freeze it before
	// another writer can replace that name, not during the later comparison.
	if !os.SameFile(before, before) {
		return true, fmt.Errorf("file identity unavailable: %s", name)
	}
	if err = r.boundary(context.Background(), "read-open", name); err != nil {
		return true, err
	}
	anchor, err := anchorPath(p, false)
	if err != nil {
		return true, err
	}
	defer func() { err = errors.Join(err, anchor.close()) }()
	f, err := anchor.openRead()
	if err != nil {
		return true, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	opened, err := f.Stat()
	if err != nil {
		return true, err
	}
	if !os.SameFile(before, opened) {
		return true, fmt.Errorf("file changed while opening: %s", name)
	}
	if err = consume(f); err != nil {
		return true, err
	}
	after, err := os.Lstat(p)
	if err != nil {
		return true, err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.ModTime() != after.ModTime() || opened.Mode() != after.Mode() {
		return true, fmt.Errorf("file changed while reading: %s", name)
	}
	return true, nil
}
func (r *Repository) hashFile(name string) (workingFile, error) {
	result := workingFile{name: name}
	p, err := r.safePath(name)
	if err != nil {
		return result, err
	}
	before, err := os.Lstat(p)
	if err != nil {
		return result, err
	}
	result.info = before
	if before.Mode()&os.ModeSymlink != 0 {
		if err = r.boundary(context.Background(), "read-link", name); err != nil {
			return result, err
		}
		link, err := readLink(p)
		if err != nil {
			return result, err
		}
		after, err := os.Lstat(p)
		if err != nil {
			return result, err
		}
		if !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
			return result, fmt.Errorf("symbolic link changed while reading: %s", name)
		}
		result.symlink, result.directoryLink = true, link.directory
		result.size, result.sum = uint64(len(link.target)), sha256.Sum256([]byte(link.target))
		return result, nil
	}
	err = r.readFile(name, func(f *os.File) error {
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(before, info) {
			return fmt.Errorf("file identity changed while hashing: %s", name)
		}
		n, sum, err := hashWorkingReader(f)
		if err != nil {
			return err
		}
		if n != info.Size() {
			return fmt.Errorf("file changed while hashing: %s", name)
		}
		result.size = uint64(n)
		result.executable = info.Mode()&0111 != 0
		result.sum = sum
		return nil
	})
	return result, err
}
func (r *Repository) scan(old []storev2.TreeEntry) ([]workingFile, error) {
	return r.scanSelected(old, []string{"."}, false)
}
func selectedPath(name string, paths []string) bool {
	for _, path := range paths {
		if path == "." || name == path || strings.HasPrefix(name, path+"/") {
			return true
		}
	}
	return false
}
func (r *Repository) scanSelected(old []storev2.TreeEntry, paths []string, trackedOnly bool) ([]workingFile, error) {
	matcher := r.ignoreMatcher()
	names := map[string]bool{}
	var walk func(string) error
	walk = func(dir string) error {
		children, err := readDirectory(dir)
		if err != nil {
			return err
		}
		for _, child := range children {
			p := filepath.Join(dir, child.Name())
			isDirectory := child.IsDir() && child.Type()&os.ModeSymlink == 0
			relative, err := filepath.Rel(r.root, p)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(relative)
			reachable := selectedPath(name, paths)
			if isDirectory && !reachable {
				for _, path := range paths {
					reachable = reachable || strings.HasPrefix(path, name+"/")
				}
			}
			if !reachable {
				continue
			}
			result, err := matcher.Check(filepath.ToSlash(relative), isDirectory)
			if err != nil {
				return err
			}
			if result.Ignored {
				continue
			}
			if err = validPath(name); err != nil {
				return err
			}
			if isDirectory {
				if err = walk(p); err != nil {
					return err
				}
			} else {
				names[name] = true
			}
		}
		return nil
	}
	if !trackedOnly {
		if err := walk(r.root); err != nil {
			return nil, err
		}
	}
	previous := map[string]storev2.TreeEntry{}
	for _, e := range old {
		if !selectedPath(e.Path, paths) {
			continue
		}
		previous[e.Path] = e
		p, err := r.safePath(e.Path)
		if errors.Is(err, errPathBlocked) {
			continue
		}
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
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			continue
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
		if e, ok := previous[name]; ok {
			if !measuresExecutable && !f.symlink && !e.Symlink {
				f.executable = e.Executable
			}
			if !measuresDirectoryLink && f.symlink && e.Symlink {
				f.directoryLink = e.DirectoryLink
			}
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
			if size != f.size || sum != f.sum || e.Executable != f.executable || e.Symlink != f.symlink || e.DirectoryLink != f.directoryLink {
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

// ingestWorking is the only working-byte ingestion path for add/commit --all.
// It never filters, decodes or normalizes bytes, including symbolic-link targets.
func (r *Repository) ingestWorking(files []workingFile) ([]storev2.TreeEntry, error) {
	ctx := context.Background()
	entries := make([]storev2.TreeEntry, 0, len(files))
	for _, file := range files {
		var id storev2.ID
		var err error
		if file.symlink {
			enabled, e := r.store.FilesystemEnabled()
			if e != nil {
				return nil, e
			}
			if !enabled {
				return nil, fmt.Errorf("symbolic links require explicit pebble enable-filesystem")
			}
			p, e := r.safePath(file.name)
			if e != nil {
				return nil, e
			}
			link, e := readLink(p)
			if e != nil {
				return nil, e
			}
			info, e := os.Lstat(p)
			if e != nil {
				return nil, e
			}
			if !sameWorkingInfo(file.info, info) || measuresDirectoryLink && link.directory != file.directoryLink {
				return nil, fmt.Errorf("link identity changed while staging: %s", file.name)
			}
			id, err = r.store.IngestFile(ctx, strings.NewReader(link.target), nil)
		} else {
			err = r.readFile(file.name, func(f *os.File) error {
				info, e := f.Stat()
				if e != nil {
					return e
				}
				if !sameWorkingInfo(file.info, info) || measuresExecutable && (info.Mode()&0111 != 0) != file.executable {
					return fmt.Errorf("file identity changed while staging: %s", file.name)
				}
				id, e = r.store.IngestFile(ctx, f, nil)
				return e
			})
		}
		if err != nil {
			return nil, err
		}
		size, sum, err := r.store.FileInfo(ctx, id)
		if err != nil {
			return nil, err
		}
		if size != file.size || sum != file.sum {
			return nil, fmt.Errorf("file changed while staging: %s", file.name)
		}
		entries = append(entries, storev2.TreeEntry{Path: file.name, Executable: file.executable, Symlink: file.symlink, DirectoryLink: file.directoryLink, Manifest: id})
	}
	return entries, nil
}

func (r *Repository) verifyWorkingOwnership(files []workingFile) error {
	for _, file := range files {
		p, err := r.safePath(file.name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !sameWorkingInfo(file.info, info) {
			return fmt.Errorf("file ownership changed before publication: %s", file.name)
		}
	}
	return nil
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
	return r.logFrom(context.Background(), id, visit)
}
