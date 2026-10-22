package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
)

// Restore explicitly discards edits to one tracked file, restoring committed HEAD.
// Untracked paths and non-regular destinations are never overwritten.
func (r *Repository) Restore(name string) error {
	if err := validFilePath(name); err != nil {
		return err
	}
	unlock, err := r.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := r.TreeEntries()
	if err != nil {
		return err
	}
	var target *TreeEntry
	for i := range entries {
		if entries[i].Name == name {
			target = &entries[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("not tracked in HEAD: %s", name)
	}
	p, err := r.safePath(name)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(p); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("restore would overwrite non-file: %s", name)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	obj, err := r.GetObject(target.Hash)
	if err != nil {
		return err
	}
	if obj.Type != ObjectTypeBlob {
		return fmt.Errorf("not a blob: %s", target.Hash)
	}
	if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return atomicWrite(p, obj.Content, fileMode(target.Mode))
}
