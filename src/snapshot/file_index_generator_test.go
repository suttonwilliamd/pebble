package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileIndexGenerator(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_file_index")
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

	// Generate index
	fig := NewFileIndexGenerator(tmpDir)
	files, err := fig.GenerateIndex()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the index
	if len(files) != len(testFiles) {
		t.Errorf("Expected %d files, got %d", len(testFiles), len(files))
	}

	// Verify file paths
	foundFiles := make(map[string]bool)
	for _, f := range files {
		foundFiles[f.Path] = true
	}

	for _, tf := range testFiles {
		if !foundFiles[tf.name] {
			t.Logf("Expected file %s not found in index", tf.name)
		}
	}

	// Verify hashes are generated
	for _, f := range files {
		if f.Hash == "" {
			t.Errorf("Hash not generated for file %s", f.Path)
		}
	}
}
