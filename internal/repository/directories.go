package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func retainedDirectoryName(i int) string { return fmt.Sprintf("old-dir-%06d", i) }
func pathParents(name string, names map[string]bool) {
	for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
		names[parent] = true
	}
}
func plannedFile(plan *worktreeIntent, name string) bool {
	for _, file := range plan.Files {
		if file.Path == name {
			return true
		}
	}
	return false
}
func plannedDirectory(plan *worktreeIntent, name string) bool {
	for _, dir := range plan.Directories {
		if dir.Path == name {
			return true
		}
	}
	return false
}
func (r *Repository) inspectLiveImage(plan *worktreeIntent, name string) (*fileImage, error) {
	p, err := r.safePath(name)
	if errors.Is(err, errPathBlocked) {
		for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if plannedFile(plan, parent) {
				return nil, nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if plannedDirectory(plan, name) {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected directory at working file: %s", name)
	}
	return inspectImage(r, name)
}

func (r *Repository) prepareDirectories(stage string, plan *worktreeIntent, target []storev2.TreeEntry) error {
	var err error
	plan.RootIdentity, err = filesystemIdentity(r.root)
	if err != nil {
		return err
	}
	parents, needed, remove := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, file := range plan.Files {
		pathParents(file.Path, parents)
	}
	for _, entry := range target {
		pathParents(entry.Path, needed)
	}
	// Only directories blocking a target leaf are removed. Unrelated empty
	// directories remain untouched. Never traverse a link while enumerating.
	var gather func(string) error
	gather = func(name string) error {
		children, err := readDirectory(filepath.Join(r.root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		remove[name] = true
		for _, child := range children {
			childName := name + "/" + child.Name()
			if err = validPath(childName); err != nil {
				return err
			}
			if child.IsDir() && child.Type()&os.ModeSymlink == 0 {
				if !parents[childName] {
					return fmt.Errorf("transition would remove untracked directory: %s", childName)
				}
				if err = gather(childName); err != nil {
					return err
				}
			} else if !plannedFile(plan, childName) {
				return fmt.Errorf("transition would overwrite untracked child: %s", childName)
			}
		}
		return nil
	}
	for _, entry := range target {
		p, err := r.safePath(entry.Path)
		if errors.Is(err, errPathBlocked) {
			continue
		}
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if err = gather(entry.Path); err != nil {
				return err
			}
		}
	}
	for name := range remove {
		parents[name] = true
	}
	names := make([]string, 0, len(parents))
	for name := range parents {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.Count(names[i], "/"), strings.Count(names[j], "/")
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	for i, name := range names {
		p, err := r.safePath(name)
		blocked := errors.Is(err, errPathBlocked)
		if err != nil && !blocked {
			return err
		}
		var info os.FileInfo
		if !blocked {
			info, err = os.Lstat(p)
		}
		staged := blocked || os.IsNotExist(err)
		if err != nil && !blocked && !os.IsNotExist(err) {
			return err
		}
		if info != nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			if !plannedFile(plan, name) {
				return fmt.Errorf("untracked file/link blocks directory: %s", name)
			}
			staged = true
		}
		if staged {
			if !needed[name] {
				return fmt.Errorf("missing source directory: %s", name)
			}
			p = filepath.Join(stage, directoryName(i))
			if err = os.Mkdir(p, 0755); err != nil {
				return err
			}
		}
		identity, err := filesystemIdentity(p)
		if err != nil {
			return err
		}
		plan.Directories = append(plan.Directories, journalDirectory{Path: name, Identity: identity, Staged: staged, Remove: remove[name]})
	}
	return nil
}

func (r *Repository) directoryAt(name string) (string, error) {
	p, err := r.safePath(name)
	if errors.Is(err, errPathBlocked) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil
	}
	return p, nil
}
func (r *Repository) inspectDirectories(stage string, plan *worktreeIntent) error {
	root, err := filesystemIdentity(r.root)
	if err != nil {
		return err
	}
	if root != plan.RootIdentity {
		return fmt.Errorf("working root replaced")
	}
	for i, dir := range plan.Directories {
		live, err := r.directoryAt(dir.Path)
		if err != nil {
			return err
		}
		slot := ""
		if dir.Staged {
			slot = directoryName(i)
		} else if dir.Remove {
			slot = retainedDirectoryName(i)
		}
		private := ""
		if slot != "" {
			private, err = (&Repository{root: stage}).directoryAt(slot)
			if err != nil {
				return err
			}
			// A file/link must not masquerade as a missing directory slot.
			if private == "" {
				if _, err = os.Lstat(filepath.Join(stage, slot)); !os.IsNotExist(err) {
					return errors.Join(fmt.Errorf("unsafe directory recovery slot: %s", slot), err)
				}
			}
		}
		if live != "" && private != "" || live == "" && private == "" {
			return fmt.Errorf("external directory change or missing recovery material: %s", dir.Path)
		}
		path := live
		if path == "" {
			path = private
		}
		identity, err := filesystemIdentity(path)
		if err != nil {
			return err
		}
		if identity != dir.Identity {
			return fmt.Errorf("external directory identity change: %s", dir.Path)
		}
		if dir.Remove {
			children, err := readDirectory(path)
			if err != nil {
				return err
			}
			if private != "" && len(children) != 0 {
				return fmt.Errorf("retained directory gained external children: %s", dir.Path)
			}
			for _, child := range children {
				name := dir.Path + "/" + child.Name()
				if !plannedFile(plan, name) && !plannedDirectory(plan, name) {
					return fmt.Errorf("external untracked directory child: %s", name)
				}
			}
		}
	}
	return nil
}
