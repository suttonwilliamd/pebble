// Tree Walker
// Purpose: Traverses the directory tree to identify conflicts.

package combine

import (
	"fmt"
	"os"
	"path/filepath"
)

type Conflict struct {
	FilePath string
	Type     string // "added", "modified", "deleted"
}

type TreeWalker struct {
	RootDir string
}

func NewTreeWalker(rootDir string) *TreeWalker {
	return &TreeWalker{RootDir: rootDir}
}

func (tw *TreeWalker) IdentifyConflicts(differences []SnapshotDifference) ([]Conflict, error) {
	var conflicts []Conflict

	for _, diff := range differences {
		filePath := filepath.Join(tw.RootDir, diff.FilePath)

		// Check if the file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			// File doesn't exist, mark as conflict if it's modified or deleted
			if diff.Type == "modified" || diff.Type == "deleted" {
				conflicts = append(conflicts, Conflict{
					FilePath: diff.FilePath,
					Type:     diff.Type,
				})
			}
		} else {
			// File exists, mark as conflict if it's modified
			if diff.Type == "modified" {
				conflicts = append(conflicts, Conflict{
					FilePath: diff.FilePath,
					Type:     diff.Type,
				})
			}
		}
	}

	return conflicts, nil
}