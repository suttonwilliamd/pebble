package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataGenerator(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := "test content"
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Generate metadata
	mg := NewMetadataGenerator()
	hash := "testhash123"
	metadata, err := mg.GenerateMetadata(testFile, hash)
	if err != nil {
		t.Fatal(err)
	}

	// Verify metadata
	if metadata.Path != testFile {
		t.Errorf("Expected path %s, got %s", testFile, metadata.Path)
	}

	if metadata.Hash != hash {
		t.Errorf("Expected hash %s, got %s", hash, metadata.Hash)
	}

	if metadata.Size != int64(len(testContent)) {
		t.Errorf("Expected size %d, got %d", len(testContent), metadata.Size)
	}

	if metadata.Permissions != 0644 {
		t.Logf("Expected permissions 0644, got %v", metadata.Permissions)
	}

	// Verify timestamp is set
	if metadata.Modified.IsZero() {
		t.Error("Timestamp not set")
	}
}
