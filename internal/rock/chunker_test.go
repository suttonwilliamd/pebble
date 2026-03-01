package rock

import (
	"bytes"
	"testing"
)

func TestFastCDC_Chunk(t *testing.T) {
	chunker := DefaultFastCDC()
	
	// Test with simple data
	data := []byte("Hello, World! This is a test of content-defined chunking.")
	chunks, err := chunker.Chunk(data)
	if err != nil {
		t.Fatalf("Chunk failed: %v", err)
	}
	
	if len(chunks) == 0 {
		t.Fatal("Expected chunks, got none")
	}
	
	// Verify we can reconstruct
	reconstructed := ReconstructData(chunks)
	if !bytes.Equal(data, reconstructed) {
		t.Errorf("Reconstructed data doesn't match original")
	}
	
	// Verify all chunks have hashes
	for i, chunk := range chunks {
		if chunk.Hash == "" {
			t.Errorf("Chunk %d has empty hash", i)
		}
	}
}

func TestFastCDC_Deduplication(t *testing.T) {
	chunker := DefaultFastCDC()
	
	// Create data with repeated sections
	data := make([]byte, 0)
	// Add same content twice
	chunk := []byte("This is repeated content that should be deduplicated.")
	data = append(data, chunk...)
	data = append(data, chunk...)
	
	chunks, err := chunker.Chunk(data)
	if err != nil {
		t.Fatalf("Chunk failed: %v", err)
	}
	
	// Check deduplication
	hashToIndices, uniqueHashes := DeduplicateChunks(chunks)
	
	t.Logf("Total chunks: %d", len(chunks))
	t.Logf("Unique hashes: %d", len(uniqueHashes))
	t.Logf("Deduplication ratio: %f", ComputeDeduplicationRatio(chunks))
	
	// With repeated content, we should see some duplicates
	for hash, indices := range hashToIndices {
		if len(indices) > 1 {
			t.Logf("Hash %s appears %d times", hash[:8], len(indices))
		}
	}
}

func TestFastCDC_LargeFile(t *testing.T) {
	chunker := DefaultFastCDC()
	
	// Create a larger test file
	size := 1024 * 1024 // 1MB
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	
	chunks, err := chunker.Chunk(data)
	if err != nil {
		t.Fatalf("Chunk failed: %v", err)
	}
	
	stats := ComputeStats(chunks)
	t.Logf("Stats: %+v", stats)
	
	// Verify reconstruction
	reconstructed := ReconstructData(chunks)
	if !bytes.Equal(data, reconstructed) {
		t.Error("Reconstructed data doesn't match")
	}
}

func TestDeduplicateChunks(t *testing.T) {
	chunks := []Chunk{
		{Hash: "abc", Size: 10},
		{Hash: "def", Size: 20},
		{Hash: "abc", Size: 10}, // Duplicate
		{Hash: "ghi", Size: 30},
	}
	
	hashToIndices, uniqueHashes := DeduplicateChunks(chunks)
	
	if len(hashToIndices) != 3 {
		t.Errorf("Expected 3 unique hashes, got %d", len(hashToIndices))
	}
	
	if len(uniqueHashes) != 3 {
		t.Errorf("Expected 3 unique hashes in set, got %d", len(uniqueHashes))
	}
	
	// Check abc appears twice
	if len(hashToIndices["abc"]) != 2 {
		t.Errorf("Expected 'abc' to appear twice, got %d times", len(hashToIndices["abc"]))
	}
}
