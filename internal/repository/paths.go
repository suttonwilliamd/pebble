package repository

import (
	"errors"
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

var errPathBlocked = errors.New("snapshot ancestor is a file or symbolic link")

func reservedWindowsName(part string) bool {
	base := part
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	base = strings.TrimRight(base, " ")
	if strings.EqualFold(base, "CON") || strings.EqualFold(base, "PRN") || strings.EqualFold(base, "AUX") || strings.EqualFold(base, "NUL") || strings.EqualFold(base, "CLOCK$") || strings.EqualFold(base, "CONIN$") || strings.EqualFold(base, "CONOUT$") {
		return true
	}
	if len(base) >= 4 && (strings.EqualFold(base[:3], "COM") || strings.EqualFold(base[:3], "LPT")) {
		switch base[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
func validPath(name string) error {
	if name == "" || len(name) > 4096 || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("unsafe snapshot path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:<>"|?*`) || strings.ContainsFunc(part, unicode.IsControl) || reservedWindowsName(part) {
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
		children, err := readDirectory(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
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
		if i < len(parts)-1 && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", fmt.Errorf("%w: %s", errPathBlocked, name)
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
