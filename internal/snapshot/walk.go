package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
)

// WalkFiles reuses the existing Pebble ignore rules without reading file bytes
// or invoking the legacy object codec. Tracked ignored paths remain the caller's
// responsibility. Symlinks and special files fail instead of being skipped.
func WalkFiles(root string, visit func(string, os.FileInfo) error) error {
	patterns, err := ReadIgnore(root)
	if err != nil {
		return err
	}
	generator := NewFileIndexGenerator(root, patterns)
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if generator.ShouldIgnore(p, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", p)
		}
		name, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return visit(filepath.ToSlash(name), info)
	})
}
