package rock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileAnalyzer(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_file_analyzer")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	textFile := filepath.Join(tmpDir, "text.txt")
	binaryFile := filepath.Join(tmpDir, "binary.bin")

	textContent := "This is a text file"
	binaryContent := []byte{0x00, 0x01, 0x02, 0x03, 0x04}

	err = os.WriteFile(textFile, []byte(textContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(binaryFile, binaryContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Test file analyzer
	fa := NewFileAnalyzer()

	// Test text file
	isBinary, err := fa.IsBinaryFile(textFile)
	if err != nil {
		t.Fatal(err)
	}
	if isBinary {
		t.Error("Text file incorrectly identified as binary")
	}

	// Test binary file
	isBinary, err = fa.IsBinaryFile(binaryFile)
	if err != nil {
		t.Fatal(err)
	}
	if !isBinary {
		t.Error("Binary file not identified as binary")
	}
}
