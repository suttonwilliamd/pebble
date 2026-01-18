package combine

import (
	"pebble/src/snapshot"
	"testing"
	"time"
)

func TestImprovedConflictDetector(t *testing.T) {
	// Create test snapshots
	snapshot1 := []snapshot.FileIndex{
		{Path: "file1.txt", Hash: "hash1", Size: 100, Modified: time.Now(), Permissions: 0644},
		{Path: "file2.txt", Hash: "hash2", Size: 200, Modified: time.Now(), Permissions: 0644},
	}

	snapshot2 := []snapshot.FileIndex{
		{Path: "file1.txt", Hash: "hash1", Size: 100, Modified: time.Now(), Permissions: 0644},
		{Path: "file2.txt", Hash: "hash2_modified", Size: 200, Modified: time.Now(), Permissions: 0644},
	}

	// Detect conflicts
	icd := NewImprovedConflictDetector()
	conflicts := icd.DetectConflicts(snapshot1, snapshot2)

	// Verify conflicts are detected
	if len(conflicts) != 1 {
		t.Errorf("Expected 1 conflict, got %d", len(conflicts))
	}

	// Verify conflict type
	if conflicts[0].Type != "modified" {
		t.Errorf("Expected conflict type 'modified', got '%s'", conflicts[0].Type)
	}

	// Verify conflict path
	if conflicts[0].Path != "file2.txt" {
		t.Errorf("Expected conflict path 'file2.txt', got '%s'", conflicts[0].Path)
	}
}
