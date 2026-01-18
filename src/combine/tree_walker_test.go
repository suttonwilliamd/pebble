package combine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreeWalker(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_tree_walker")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	testFiles := []struct {
		name    string
		content string
	}{
		{"file1.txt", "content1"},
		{"file2.txt", "content2"},
		{"subdir/file3.txt", "content3"},
		{"conflict.theirs", "theirs content"},
		{"conflict.ours", "ours content"},
	}

	for _, tf := range testFiles {
		filePath := filepath.Join(tmpDir, tf.name)
		err := os.MkdirAll(filepath.Dir(filePath), 0755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(filePath, []byte(tf.content), 0644)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Walk directory
	tw := NewTreeWalker()
	conflicts, err := tw.WalkDirectory(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify conflicts are detected
	if len(conflicts) != 2 {
		t.Errorf("Expected 2 conflicts, got %d", len(conflicts))
	}

	// Verify conflict types
	for _, conflict := range conflicts {
		if conflict.Type != "conflict" {
			t.Errorf("Expected conflict type 'conflict', got '%s'", conflict.Type)
		}
	}
}
