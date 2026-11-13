package repository

import (
	"fmt"
	"path/filepath"
)

func (r *Repository) preflightPermissions(stage string, plan *worktreeIntent) error {
	write := map[string]bool{}
	for _, file := range plan.Files {
		if !file.Keep && (file.Original != nil || file.Target != nil) {
			write[filepath.ToSlash(filepath.Dir(file.Path))] = true
		}
	}
	for _, dir := range plan.Directories {
		if dir.Remove || dir.Staged {
			write[filepath.ToSlash(filepath.Dir(dir.Path))] = true
		}
		if dir.Remove {
			// Cross-parent directory rename updates "..", requiring write
			// access to the directory itself on POSIX, not just its parents.
			write[dir.Path] = true
		}
	}
	if err := preflightDirectory(r.root, write["."]); err != nil {
		return fmt.Errorf("working-root durability/permission preflight: %w", err)
	}
	if err := preflightDirectory(filepath.Dir(stage), true); err != nil {
		return fmt.Errorf("journal-parent durability/permission preflight: %w", err)
	}
	for i, dir := range plan.Directories {
		path := filepath.Join(r.root, filepath.FromSlash(dir.Path))
		if dir.Staged {
			path = filepath.Join(stage, directoryName(i))
		}
		if err := preflightDirectory(path, write[dir.Path]); err != nil {
			return fmt.Errorf("directory %s durability/permission preflight: %w", dir.Path, err)
		}
	}
	return nil
}
