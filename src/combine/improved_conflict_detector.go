package combine

import (
	"pebble/src/snapshot"
)

// ImprovedConflictDetector is responsible for detecting conflicts accurately
type ImprovedConflictDetector struct {
}

// NewImprovedConflictDetector creates a new ImprovedConflictDetector instance
func NewImprovedConflictDetector() *ImprovedConflictDetector {
	return &ImprovedConflictDetector{}
}

// DetectConflicts detects conflicts between two snapshots
func (icd *ImprovedConflictDetector) DetectConflicts(snapshot1, snapshot2 []snapshot.FileIndex) []Conflict {
	var conflicts []Conflict

	// Create a map of files in snapshot1 for easy lookup
	snapshot1Files := make(map[string]snapshot.FileIndex)
	for _, file := range snapshot1 {
		snapshot1Files[file.Path] = file
	}

	// Identify modified files
	for _, file := range snapshot2 {
		if existingFile, exists := snapshot1Files[file.Path]; exists {
			if existingFile.Hash != file.Hash {
				conflicts = append(conflicts, Conflict{
					Path: file.Path,
					Type: "modified",
				})
			}
		}
	}

	return conflicts
}
