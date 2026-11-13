package snapshot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/suttonwilliamd/pebble/internal/ignore"
)

type FileIndexGenerator struct {
	rootPath string
	matcher  *ignore.Matcher
}

func NewFileIndexGenerator(root string) *FileIndexGenerator {
	return &FileIndexGenerator{rootPath: root, matcher: ignore.New(func(name string) ([]byte, error) {
		p := filepath.Join(root, filepath.FromSlash(name))
		before, err := os.Lstat(p)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if !before.Mode().IsRegular() {
			return nil, fmt.Errorf("not a regular ignore file: %s", name)
		}
		// Freeze Windows' lazy pathname identity before opening the rule file.
		if !os.SameFile(before, before) {
			return nil, fmt.Errorf("ignore file identity unavailable: %s", name)
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !os.SameFile(before, opened) {
			return nil, fmt.Errorf("ignore file changed while opening: %s", name)
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		after, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.ModTime() != after.ModTime() || opened.Mode() != after.Mode() {
			return nil, fmt.Errorf("ignore file changed while reading: %s", name)
		}
		return data, nil
	})}
}

func (f *FileIndexGenerator) shouldIgnore(file string, isDir bool) (bool, error) {
	rel, err := filepath.Rel(f.rootPath, file)
	if err != nil {
		return false, err
	}
	result, err := f.matcher.Check(filepath.ToSlash(rel), isDir)
	return result.Ignored, err
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
		ignored, err := f.shouldIgnore(file, info.IsDir())
		if err != nil {
			return err
		}
		if ignored {
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
