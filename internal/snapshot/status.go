package snapshot

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Change is a working-tree difference from the current branch's HEAD snapshot.
type Change struct {
	Path string
	Kind string // added, modified, or deleted
}

// Status describes HEAD and the current working tree. Pebble has no staging area.
type Status struct {
	Branch  string
	Unborn  bool
	Changes []Change
}

func validObjectHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func checkedObject(repo *Repository, hash string, kind ObjectType) (*Object, error) {
	if !validObjectHash(hash) {
		return nil, fmt.Errorf("invalid %s hash %q", kind, hash)
	}
	obj, err := repo.GetObject(hash)
	if err != nil {
		return nil, err
	}
	if obj.Type != kind || obj.Hash != hash || ComputeHash(obj.Content) != hash || obj.Size != int64(len(obj.Content)) {
		return nil, fmt.Errorf("invalid or corrupt %s object %s", kind, hash)
	}
	return obj, nil
}

func headEntries(repo *Repository) (string, bool, map[string]TreeEntry, error) {
	ref, err := repo.GetHead()
	if err != nil {
		return "", false, nil, err
	}
	if !strings.HasPrefix(ref, "heads/") || strings.TrimPrefix(ref, "heads/") == "" || path.Clean(ref) != ref || strings.ContainsAny(ref, "\\\x00:") {
		return "", false, nil, fmt.Errorf("invalid branch reference %q", ref)
	}
	if _, err := os.Stat(filepath.Join(repo.refsPath, filepath.FromSlash(ref))); err != nil {
		return "", false, nil, fmt.Errorf("read HEAD reference: %w", err)
	}
	hash, err := repo.GetRef(ref)
	if err != nil {
		return "", false, nil, err
	}
	entries := make(map[string]TreeEntry)
	if hash == "" {
		return strings.TrimPrefix(ref, "heads/"), true, entries, nil
	}
	obj, err := checkedObject(repo, hash, ObjectTypeCommit)
	if err != nil {
		return "", false, nil, err
	}
	var commit Commit
	if err := json.Unmarshal(obj.Content, &commit); err != nil {
		return "", false, nil, fmt.Errorf("read HEAD commit: %w", err)
	}
	obj, err = checkedObject(repo, commit.Tree, ObjectTypeTree)
	if err != nil {
		return "", false, nil, err
	}
	var tree []TreeEntry
	if err := json.Unmarshal(obj.Content, &tree); err != nil {
		return "", false, nil, fmt.Errorf("read HEAD tree: %w", err)
	}
	for _, entry := range tree {
		// Accept historical Windows snapshots; new indices use portable slashes.
		entry.Name = strings.ReplaceAll(entry.Name, "\\", "/")
		if entry.Name == "." || path.Clean(entry.Name) != entry.Name || strings.HasPrefix(entry.Name, "/") || strings.HasPrefix(entry.Name, "../") || strings.ContainsAny(entry.Name, "\x00:") || metadataPath(entry.Name) {
			return "", false, nil, fmt.Errorf("invalid tree path %q", entry.Name)
		}
		if entry.Type != ObjectTypeBlob || !validObjectHash(entry.Hash) {
			return "", false, nil, fmt.Errorf("invalid tree entry %q", entry.Name)
		}
		if _, duplicate := entries[entry.Name]; duplicate {
			return "", false, nil, fmt.Errorf("duplicate tree path %q", entry.Name)
		}
		entries[entry.Name] = entry
	}
	return strings.TrimPrefix(ref, "heads/"), false, entries, nil
}

func workingIndex(repo *Repository, tracked map[string]TreeEntry) (*Index, error) {
	patterns, err := LoadIgnorePatterns(repo.RootPath())
	if err != nil {
		return nil, err
	}
	generator := NewFileIndexGenerator(repo.RootPath(), patterns)
	generator.trackedPaths = make(map[string]bool, len(tracked))
	for name := range tracked {
		generator.trackedPaths[name] = true
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			generator.trackedPaths[dir] = true
		}
	}
	return generator.GenerateIndex()
}

// WorkingIndex generates a commit index, retaining already tracked files even
// when a newly added ignore rule matches them. It uses the same rules as status.
func WorkingIndex(repo *Repository) (*Index, error) {
	_, _, tracked, err := headEntries(repo)
	if err != nil {
		return nil, err
	}
	return workingIndex(repo, tracked)
}

func executable(mode string) bool { return strings.Contains(mode, "x") }

// WorkingTreeStatus compares file content and executable bits with HEAD, not
// the saved index (which can belong to another branch). It performs no writes.
func WorkingTreeStatus(repo *Repository) (*Status, error) {
	branch, unborn, tracked, err := headEntries(repo)
	if err != nil {
		return nil, err
	}
	index, err := workingIndex(repo, tracked)
	if err != nil {
		return nil, err
	}
	result := &Status{Branch: branch, Unborn: unborn}
	for _, entry := range index.Entries {
		if entry.Hash == "" {
			continue
		} // Directories and unsupported symlinks.
		old, exists := tracked[entry.Path]
		if !exists {
			result.Changes = append(result.Changes, Change{entry.Path, "added"})
		} else if old.Hash != entry.Hash || executable(old.Mode) != executable(entry.Mode) {
			result.Changes = append(result.Changes, Change{entry.Path, "modified"})
		}
		delete(tracked, entry.Path)
	}
	for name := range tracked {
		result.Changes = append(result.Changes, Change{name, "deleted"})
	}
	sort.Slice(result.Changes, func(i, j int) bool { return result.Changes[i].Path < result.Changes[j].Path })
	return result, nil
}
