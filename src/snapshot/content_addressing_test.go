package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContentAddressing(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_content_addressing")
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

	// Generate hash
	ca := NewContentAddressing()
	hash, err := ca.GenerateHash(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify hash is generated
	if hash == "" {
		t.Error("Hash not generated")
	}

	// Verify hash is consistent
	hash2, err := ca.GenerateHash(testFile)
	if err != nil {
		t.Fatal(err)
	}

	if hash != hash2 {
		t.Error("Hash is not consistent")
	}
}
