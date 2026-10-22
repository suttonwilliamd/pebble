package snapshot

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type FileIndexGenerator struct {
	rootPath       string
	ignorePatterns []string
}

func NewFileIndexGenerator(root string, patterns []string) *FileIndexGenerator {
	return &FileIndexGenerator{root, patterns}
}

// ReadIgnore supports comments, blank lines, globs, root-relative paths and directory rules.
// Negation and ** are rejected instead of silently misinterpreted.
func ReadIgnore(root string) ([]string, error) {
	patterns := []string{".pebble/", ".git/"}
	data, err := os.ReadFile(filepath.Join(root, ".pebbleignore"))
	if os.IsNotExist(err) {
		return patterns, nil
	}
	if err != nil {
		return nil, err
	}
	for n, line := range strings.Split(string(data), "\n") {
		p := strings.TrimSpace(line)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		if strings.HasPrefix(p, "!") || strings.Contains(p, "**") || strings.Contains(p, "\\") {
			return nil, fmt.Errorf(".pebbleignore:%d: negation, ** and backslashes are not supported", n+1)
		}
		if _, err := path.Match(strings.Trim(p, "/"), ""); err != nil {
			return nil, fmt.Errorf(".pebbleignore:%d: %w", n+1, err)
		}
		patterns = append(patterns, p)
	}
	return patterns, nil
}
func (f *FileIndexGenerator) shouldIgnore(file string, isDir bool) bool {
	rel, err := filepath.Rel(f.rootPath, file)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(rel)
	// Metadata must never become part of a snapshot, even without ignore patterns.
	for _, part := range strings.Split(rel, "/") {
		if strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git") {
			return true
		}
	}
	for _, p := range f.ignorePatterns {
		p = filepath.ToSlash(p)
		dirOnly := strings.HasSuffix(p, "/")
		if dirOnly && !isDir {
			continue
		}
		anchored := strings.HasPrefix(p, "/")
		p = strings.Trim(p, "/")
		candidate := rel
		if !anchored && !strings.Contains(p, "/") {
			candidate = path.Base(rel)
		}
		if match, _ := path.Match(p, candidate); match {
			return true
		}
	}
	return false
}
func (f *FileIndexGenerator) GenerateIndex() (*Index, error) {
	entries := []IndexEntry{}
	err := filepath.Walk(f.rootPath, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if file == f.rootPath {
			return nil
		}
		if f.shouldIgnore(file, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type at %s (symlinks and special files are not yet supported)", file)
		}
		rel, err := filepath.Rel(f.rootPath, file)
		if err != nil {
			return err
		}
		obj, err := CreateBlob(file)
		if err != nil {
			return err
		}
		entries = append(entries, IndexEntry{Path: filepath.ToSlash(rel), Mode: info.Mode().String(), Size: obj.Size, Mtime: info.ModTime(), Hash: obj.Hash})
		return nil
	})
	if err != nil {
		return nil, err
	}
	obj := CreateIndex(entries)
	return &Index{Version: 2, Generator: "pebble/1.0", Entries: entries, Hash: obj.Hash}, nil
}
func (f *FileIndexGenerator) GetFileHashes() (map[string]string, error) {
	idx, err := f.GenerateIndex()
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, e := range idx.Entries {
		m[e.Path] = e.Hash
	}
	return m, nil
}
func (f *FileIndexGenerator) GetModifiedFiles(since time.Time) ([]string, error) {
	idx, err := f.GenerateIndex()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range idx.Entries {
		if e.Mtime.After(since) {
			files = append(files, e.Path)
		}
	}
	return files, nil
}
