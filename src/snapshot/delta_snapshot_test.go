package snapshot

import (
	"testing"
	"time"
)

func TestDeltaSnapshot(t *testing.T) {
	// Create test snapshots
	baseSnapshot := []FileIndex{
		{Path: "file1.txt", Hash: "hash1", Size: 100, Modified: time.Now(), Permissions: 0644},
		{Path: "file2.txt", Hash: "hash2", Size: 200, Modified: time.Now(), Permissions: 0644},
	}

	newSnapshot := []FileIndex{
		{Path: "file1.txt", Hash: "hash1", Size: 100, Modified: time.Now(), Permissions: 0644},
		{Path: "file3.txt", Hash: "hash3", Size: 300, Modified: time.Now(), Permissions: 0644},
	}

	// Create delta snapshot
	dsc := NewDeltaSnapshotCreator()
	deltaSnapshot := dsc.CreateDeltaSnapshot(baseSnapshot, newSnapshot)

	// Verify added files
	if len(deltaSnapshot.AddedFiles) != 1 {
		t.Errorf("Expected 1 added file, got %d", len(deltaSnapshot.AddedFiles))
	}

	// Verify modified files
	if len(deltaSnapshot.ModifiedFiles) != 0 {
		t.Errorf("Expected 0 modified files, got %d", len(deltaSnapshot.ModifiedFiles))
	}

	// Verify deleted files
	if len(deltaSnapshot.DeletedFiles) != 1 {
		t.Errorf("Expected 1 deleted file, got %d", len(deltaSnapshot.DeletedFiles))
	}

	// Retrieve full snapshot
	dsr := NewDeltaSnapshotRetriever()
	fullSnapshot := dsr.RetrieveFullSnapshot(baseSnapshot, deltaSnapshot)

	// Verify full snapshot
	if len(fullSnapshot) != 2 {
		t.Errorf("Expected 2 files in full snapshot, got %d", len(fullSnapshot))
	}

	// Verify file paths
	foundFiles := make(map[string]bool)
	for _, file := range fullSnapshot {
		foundFiles[file.Path] = true
	}

	if !foundFiles["file1.txt"] {
		t.Error("Expected file file1.txt not found in full snapshot")
	}

	if !foundFiles["file3.txt"] {
		t.Error("Expected file file3.txt not found in full snapshot")
	}

	if foundFiles["file2.txt"] {
		t.Error("Unexpected file file2.txt found in full snapshot")
	}
}
