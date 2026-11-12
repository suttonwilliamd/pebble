package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func sortRefs(refs []Ref) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
}
func validPath(name string) error {
	if name == "" || len(name) > 4096 || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("unsafe snapshot path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:<>"|?*`) || strings.ContainsFunc(part, unicode.IsControl) || reserved {
			return fmt.Errorf("unsafe or nonportable snapshot path %q", name)
		}
	}
	return nil
}
func validateEntries(entries []storev2.TreeEntry) error {
	seen := map[string]bool{}
	spellings := map[string]string{}
	for _, e := range entries {
		if err := validPath(e.Path); err != nil {
			return err
		}
		for prefix := e.Path; prefix != "."; prefix = filepath.ToSlash(filepath.Dir(prefix)) {
			key := strings.ToLower(prefix)
			if previous, ok := spellings[key]; ok && previous != prefix {
				return fmt.Errorf("case-colliding path components: %s and %s", previous, prefix)
			}
			spellings[key] = prefix
		}
		key := strings.ToLower(e.Path)
		if seen[key] {
			return fmt.Errorf("duplicate or case-colliding path: %s", e.Path)
		}
		seen[key] = true
	}
	for name := range seen {
		for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if seen[parent] {
				return fmt.Errorf("file/directory collision: %s", parent)
			}
		}
	}
	return nil
}
func (r *Repository) safePath(name string) (string, error) {
	if err := validPath(name); err != nil {
		return "", err
	}
	p := r.root
	parts := strings.Split(name, "/")
	for i, part := range parts {
		// Refuse differently spelled filesystem aliases, including Unicode
		// normalization aliases, without rewriting canonical tree paths.
		children, err := os.ReadDir(p)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		exact := false
		for _, child := range children {
			if child.Name() == part {
				exact = true
			}
			if child.Name() != part && strings.EqualFold(child.Name(), part) {
				return "", fmt.Errorf("case-colliding working path: %s", name)
			}
		}
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !exact {
			return "", fmt.Errorf("filesystem-alias working path: %s", name)
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

// Probe the target namespace before touching working files. Exclusive creates
// and exact directory spellings use this filesystem's alias rules rather than
// imposing Unicode normalization on format2 paths that can be distinct elsewhere.
func preflightFilesystemPaths(stage string, entries []storev2.TreeEntry) error {
	root := filepath.Join(stage, "paths")
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	probe := Repository{root: root}
	for _, entry := range entries {
		path, err := probe.safePath(entry.Path)
		if err != nil {
			return fmt.Errorf("checkout cannot represent path %q: %w", entry.Path, err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("checkout cannot represent path %q: %w", entry.Path, err)
		}
		if err = file.Close(); err != nil {
			return err
		}
		if _, err = probe.safePath(entry.Path); err != nil {
			return fmt.Errorf("checkout cannot represent path %q: %w", entry.Path, err)
		}
	}
	return nil
}
func mode(executable bool) os.FileMode {
	if executable {
		return 0755
	}
	return 0644
}
