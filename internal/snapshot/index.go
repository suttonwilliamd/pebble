package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileIndexGenerator generates file indices for snapshots
type FileIndexGenerator struct {
	rootPath       string
	ignorePatterns []string
	trackedPaths   map[string]bool
}

// NewFileIndexGenerator creates a new index generator
func NewFileIndexGenerator(rootPath string, ignorePatterns []string) *FileIndexGenerator {
	return &FileIndexGenerator{
		rootPath:       rootPath,
		ignorePatterns: ignorePatterns,
	}
}

// shouldIgnore checks if a path should be ignored
func (f *FileIndexGenerator) shouldIgnore(path string, isDir bool) bool {
	relPath, err := filepath.Rel(f.rootPath, path)
	if err != nil {
		return true
	}

	relPath = filepath.ToSlash(relPath)
	if metadataPath(relPath) {
		return true
	}
	if f.trackedPaths[relPath] {
		return false
	}
	return matchesIgnore(relPath, isDir, f.ignorePatterns)
}

// GenerateIndex generates a file index for the root path
func (f *FileIndexGenerator) GenerateIndex() (*Index, error) {
	for _, pattern := range f.ignorePatterns {
		if err := validateIgnorePattern(pattern); err != nil {
			return nil, err
		}
	}
	entries := []IndexEntry{}

	err := filepath.Walk(f.rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root path itself
		if path == f.rootPath {
			return nil
		}

		// Check if should ignore
		if f.shouldIgnore(path, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(f.rootPath, path)
		if err != nil {
			return err
		}

		// Get file mode
		mode := info.Mode()

		// Skip symlinks for now
		if mode&os.ModeSymlink != 0 {
			return nil
		}

		entry := IndexEntry{
			Path:  filepath.ToSlash(relPath),
			Mode:  mode.String(),
			Size:  info.Size(),
			Mtime: info.ModTime(),
		}

		// If it's a regular file, compute hash. Literal backslashes/colons in
		// POSIX filenames cannot be represented by our portable tree paths.
		if info.Mode().IsRegular() {
			if strings.ContainsAny(entry.Path, "\\:\x00") {
				return fmt.Errorf("unsupported portable file path %q", entry.Path)
			}
			obj, err := CreateBlob(path)
			if err != nil {
				return fmt.Errorf("failed to create blob for %s: %w", path, err)
			}
			entry.Hash = obj.Hash
		}

		entries = append(entries, entry)

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	index := CreateIndex(entries)

	// Convert to Index struct
	result := &Index{
		Version:   2,
		Generator: "pebble/1.0",
		Entries:   entries,
		Hash:      index.Hash,
	}

	return result, nil
}

// GetFileHashes returns a map of file paths to their hashes
func (f *FileIndexGenerator) GetFileHashes() (map[string]string, error) {
	index, err := f.GenerateIndex()
	if err != nil {
		return nil, err
	}

	hashes := make(map[string]string)
	for _, entry := range index.Entries {
		hashes[entry.Path] = entry.Hash
	}

	return hashes, nil
}

// GetModifiedFiles returns files modified since the given time
func (f *FileIndexGenerator) GetModifiedFiles(since time.Time) ([]string, error) {
	index, err := f.GenerateIndex()
	if err != nil {
		return nil, err
	}

	var modified []string
	for _, entry := range index.Entries {
		if entry.Mtime.After(since) {
			modified = append(modified, entry.Path)
		}
	}

	return modified, nil
}
