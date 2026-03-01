package combine

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	
	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

// ConflictType represents the type of conflict
type ConflictType string

const (
	ConflictTypeBothModified ConflictType = "both_modified"
	ConflictTypeDeleted     ConflictType = "deleted"
	ConflictTypeAdded      ConflictType = "added"
	ConflictTypeRenamed    ConflictType = "renamed"
)

// FileConflict represents a conflict between two versions
type FileConflict struct {
	Path         string
	ConflictType ConflictType
	BaseHash     string
	OurHash      string
	TheirHash    string
	BaseContent  []byte
	OurContent   []byte
	TheirContent []byte
}

// Resolution represents a conflict resolution
type Resolution struct {
	Path    string
	Content []byte
	Action  string // "ours", "theirs", "merge", "delete"
}

// Comparator compares two snapshots
type Comparator struct {
	baseDir string
}

// NewComparator creates a new comparator
func NewComparator(baseDir string) *Comparator {
	return &Comparator{baseDir: baseDir}
}

// CompareTree compares two tree hashes and returns conflicts
func (c *Comparator) CompareTree(repo *snapshot.Repository, baseHash, ourHash, theirHash string) ([]FileConflict, error) {
	// Load trees
	baseTree, ourTree, theirTree, err := c.loadTrees(repo, baseHash, ourHash, theirHash)
	if err != nil {
		return nil, err
	}
	
	// Build maps for easier lookup
	baseMap := treeToMap(baseTree)
	ourMap := treeToMap(ourTree)
	theirMap := treeToMap(theirTree)
	
	var conflicts []FileConflict
	
	// Check all paths
	allPaths := make(map[string]bool)
	for k := range baseMap {
		allPaths[k] = true
	}
	for k := range ourMap {
		allPaths[k] = true
	}
	for k := range theirMap {
		allPaths[k] = true
	}
	
	for path := range allPaths {
		baseEntry, baseOk := baseMap[path]
		ourEntry, ourOk := ourMap[path]
		theirEntry, theirOk := theirMap[path]
		
		// Determine conflict type
		if ourOk && theirOk {
			// Both have the file
			if baseOk {
				// Both modified
				if ourEntry.Hash != theirEntry.Hash && ourEntry.Hash != baseEntry.Hash && theirEntry.Hash != baseEntry.Hash {
					conflict := FileConflict{
						Path:         path,
						ConflictType: ConflictTypeBothModified,
						BaseHash:     baseEntry.Hash,
						OurHash:      ourEntry.Hash,
						TheirHash:    theirEntry.Hash,
					}
					conflicts = append(conflicts, conflict)
				}
			} else {
				// Both added - no conflict
			}
		} else if ourOk && !theirOk {
			// Deleted in theirs
			if baseOk {
				conflicts = append(conflicts, FileConflict{
					Path:         path,
					ConflictType: ConflictTypeDeleted,
					BaseHash:     baseEntry.Hash,
					OurHash:      ourEntry.Hash,
					TheirHash:    "",
				})
			}
		} else if !ourOk && theirOk {
			// Deleted in ours
			if baseOk {
				conflicts = append(conflicts, FileConflict{
					Path:         path,
					ConflictType: ConflictTypeDeleted,
					BaseHash:     baseEntry.Hash,
					OurHash:      "",
					TheirHash:    theirEntry.Hash,
				})
			}
		}
	}
	
	return conflicts, nil
}

// treeToMap converts a tree to a map of path -> entry
func treeToMap(tree *snapshot.Tree) map[string]snapshot.TreeEntry {
	result := make(map[string]snapshot.TreeEntry)
	for _, entry := range tree.Entries {
		result[entry.Name] = entry
	}
	return result
}

// loadTrees loads base, our, and their trees
func (c *Comparator) loadTrees(repo *snapshot.Repository, baseHash, ourHash, theirHash string) (*snapshot.Tree, *snapshot.Tree, *snapshot.Tree, error) {
	emptyTree := &snapshot.Tree{}
	
	if baseHash == "" {
		emptyTree = &snapshot.Tree{}
	} else {
		// Would load and unmarshal tree here
	}
	
	if ourHash == "" {
		emptyTree = &snapshot.Tree{}
	} else {
		// Would load and unmarshal tree here
	}
	
	if theirHash == "" {
		emptyTree = &snapshot.Tree{}
	} else {
		// Would load and unmarshal tree here
	}
	
	return emptyTree, emptyTree, emptyTree, nil
}

// ResolveConflict resolves a conflict based on strategy
func ResolveConflict(conflict FileConflict, strategy string) Resolution {
	res := Resolution{
		Path:   conflict.Path,
		Action: strategy,
	}
	
	switch strategy {
	case "ours":
		res.Content = conflict.OurContent
	case "theirs":
		res.Content = conflict.TheirContent
	case "merge":
		// Simple merge - would need 3-way merge in real implementation
		if len(conflict.OurContent) > 0 {
			res.Content = conflict.OurContent
		} else {
			res.Content = conflict.TheirContent
		}
	case "delete":
		res.Content = nil
	default:
		res.Content = conflict.BaseContent
	}
	
	return res
}

// MaterializeConflict writes conflict files for manual resolution
func MaterializeConflict(conflict FileConflict, baseDir string) error {
	conflictDir := filepath.Join(baseDir, ".pebble", "conflicts")
	if err := os.MkdirAll(conflictDir, 0755); err != nil {
		return err
	}
	
	// Create conflict file with markers
	var buf bytes.Buffer
	
	buf.WriteString("<<<<<<< OURS\n")
	if len(conflict.OurContent) > 0 {
		buf.Write(conflict.OurContent)
	}
	buf.WriteString("=======\n")
	if len(conflict.TheirContent) > 0 {
		buf.Write(conflict.TheirContent)
	}
	buf.WriteString(">>>>>>> THEIRS\n")
	
	conflictPath := filepath.Join(conflictDir, conflict.Path+".conflict")
	
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0755); err != nil {
		return err
	}
	
	return os.WriteFile(conflictPath, buf.Bytes(), 0644)
}

// ApplyResolution applies a resolution to the working directory
func ApplyResolution(res Resolution, baseDir string) error {
	if res.Action == "delete" || res.Content == nil {
		// Delete the file
		filePath := filepath.Join(baseDir, res.Path)
		return os.Remove(filePath)
	}
	
	// Write the resolved content
	filePath := filepath.Join(baseDir, res.Path)
	
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	
	return os.WriteFile(filePath, res.Content, 0644)
}

// TreeWalker walks a tree and performs operations
type TreeWalker struct {
	rootPath string
}

// NewTreeWalker creates a new tree walker
func NewTreeWalker(rootPath string) *TreeWalker {
	return &TreeWalker{rootPath: rootPath}
}

// Walk walks the tree and calls the callback for each entry
func (tw *TreeWalker) Walk(tree *snapshot.Tree, callback func(entry snapshot.TreeEntry) error) error {
	for _, entry := range tree.Entries {
		if err := callback(entry); err != nil {
			return err
		}
	}
	return nil
}

// FileComparator compares two files
type FileComparator struct{}

// NewFileComparator creates a new file comparator
func NewFileComparator() *FileComparator {
	return &FileComparator{}
}

// CompareContent compares two sets of content
func (fc *FileComparator) CompareContent(ourContent, theirContent []byte) (bool, error) {
	return bytes.Equal(ourContent, theirContent), nil
}

// DiffLines returns the diff between two contents
func (fc *FileComparator) DiffLines(ourContent, theirContent []byte) (ourLines, theirLines []string) {
	ourLines = strings.Split(string(ourContent), "\n")
	theirLines = strings.Split(string(theirContent), "\n")
	sort.Strings(ourLines)
	sort.Strings(theirLines)
	return
}
