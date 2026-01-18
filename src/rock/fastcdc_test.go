package rock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFastCDC(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_fastcdc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test file
	testFile := filepath.Join(tmpDir, "test.bin")
	testContent := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Test FastCDC
	fastCDC := NewFastCDC(1024, 4096, 16384)
	chunks, err := fastCDC.ChunkFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify chunks are generated
	if len(chunks) == 0 {
		t.Error("No chunks generated")
	}

	// Verify chunk hashes are generated
	for _, chunk := range chunks {
		if chunk.Hash == "" {
			t.Errorf("Hash not generated for chunk with data length %d", len(chunk.Data))
		}
	}
}
