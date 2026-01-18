package combine

import (
	"pebble/src/snapshot"
	"testing"
	"time"
)

func TestSnapshotComparator(t *testing.T) {
	// Create test snapshots
	snapshot1 := []snapshot.FileIndex{
		{Path: "file1.txt", Hash: "hash1", Size: 100, Modified: time.Now(), Permissions: 0644},
		{Path: "file2.txt", Hash: "hash2", Size: 200, Modified: time.Now(), Permissions: 0644},
	}

	snapshot2 := []snapshot.FileIndex{
		{Path: "file1.txt", Hash: "hash1", Size: 100, Modified: time.Now(), Permissions: 0644},
		{Path: "file3.txt", Hash: "hash3", Size: 300, Modified: time.Now(), Permissions: 0644},
	}

	// Compare snapshots
	sc := NewSnapshotComparator()
	comparison := sc.CompareSnapshots(snapshot1, snapshot2)

	// Verify added files
	if len(comparison.AddedFiles) != 1 {
		t.Errorf("Expected 1 added file, got %d", len(comparison.AddedFiles))
	}

	// Verify modified files
	if len(comparison.ModifiedFiles) != 0 {
		t.Errorf("Expected 0 modified files, got %d", len(comparison.ModifiedFiles))
	}

	// Verify deleted files
	if len(comparison.DeletedFiles) != 1 {
		t.Errorf("Expected 1 deleted file, got %d", len(comparison.DeletedFiles))
	}
}
