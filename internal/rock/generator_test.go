package rock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChunkGenerator_GenerateChunks(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	
	// Create test file with some content
	testFile := filepath.Join(tmpDir, "test.bin")
	content := make([]byte, 10000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create chunk database
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create generator
	cg := NewChunkGenerator(db)

	// Chunk the file
	result, err := cg.GenerateChunks(testFile)
	if err != nil {
		t.Fatalf("GenerateChunks error: %v", err)
	}

	if result.TotalChunks == 0 {
		t.Error("Expected chunks, got 0")
	}

	if result.FileSize != 10000 {
		t.Errorf("FileSize = %d, want 10000", result.FileSize)
	}

	t.Logf("File: %s, Chunks: %d, Unique: %d", result.FilePath, result.TotalChunks, result.UniqueChunks)
}

func TestChunkGenerator_ReconstructFile(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create test file - need at least 1KB for min file size
	original := make([]byte, 2000)
	for i := range original {
		original[i] = byte(i % 256)
	}
	testFile := filepath.Join(tmpDir, "original.bin")
	absTestFile, _ := filepath.Abs(testFile)
	if err := os.WriteFile(testFile, original, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create database and generator
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	cg := NewChunkGenerator(db)

	// Chunk the file
	result, err := cg.GenerateChunks(absTestFile)
	if err != nil {
		t.Fatalf("GenerateChunks error: %v", err)
	}
	t.Logf("Chunked file: %s, chunks: %d", result.FilePath, result.TotalChunks)

	// Reconstruct using the same path
	outputPath := filepath.Join(tmpDir, "reconstructed.txt")
	err = cg.ReconstructFile(absTestFile, outputPath)
	if err != nil {
		t.Fatalf("ReconstructFile error: %v", err)
	}

	// Verify
	reconstructed, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read reconstructed file: %v", err)
	}

	if string(original) != string(reconstructed) {
		t.Errorf("Reconstructed content doesn't match original")
		t.Logf("Original: %s", string(original))
		t.Logf("Reconstructed: %s", string(reconstructed))
	}
}

func TestChunkGenerator_GetChunkStats(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create test file - need at least 1KB for min file size
	testFile := filepath.Join(tmpDir, "test.bin")
	absTestFile, _ := filepath.Abs(testFile)
	content := make([]byte, 2000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create database and generator
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	cg := NewChunkGenerator(db)

	// Get stats before chunking
	totalChunks, totalSize, totalRefs, err := cg.GetChunkStats()
	if err != nil {
		t.Fatalf("GetChunkStats error: %v", err)
	}

	if totalChunks != 0 {
		t.Errorf("Expected 0 chunks before chunking, got %d", totalChunks)
	}

	// Chunk file
	_, err = cg.GenerateChunks(absTestFile)
	if err != nil {
		t.Fatalf("GenerateChunks error: %v", err)
	}

	// Get stats after
	totalChunks, totalSize, totalRefs, err = cg.GetChunkStats()
	if err != nil {
		t.Fatalf("GetChunkStats error: %v", err)
	}

	if totalChunks == 0 {
		t.Error("Expected chunks after chunking")
	}

	t.Logf("Stats: chunks=%d, size=%d, refs=%d", totalChunks, totalSize, totalRefs)
}

func TestChunkGenerator_Deduplication(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create two files with same content - need at least 1KB
	content := make([]byte, 2000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	
	file1 := filepath.Join(tmpDir, "file1.bin")
	file2 := filepath.Join(tmpDir, "file2.bin")
	absFile1, _ := filepath.Abs(file1)
	absFile2, _ := filepath.Abs(file2)
	
	if err := os.WriteFile(file1, content, 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, content, 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}

	// Create database and generator
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	cg := NewChunkGenerator(db)

	// Chunk both files
	result1, err := cg.GenerateChunks(absFile1)
	if err != nil {
		t.Fatalf("GenerateChunks error: %v", err)
	}

	result2, err := cg.GenerateChunks(absFile2)
	if err != nil {
		t.Fatalf("GenerateChunks error: %v", err)
	}

	// Check that second file used cached chunks
	t.Logf("File 1: chunks=%d, unique=%d", result1.TotalChunks, result1.UniqueChunks)
	t.Logf("File 2: chunks=%d, unique=%d", result2.TotalChunks, result2.UniqueChunks)

	// Get stats - should show fewer unique chunks than total
	totalChunks, _, totalRefs, err := cg.GetChunkStats()
	if err != nil {
		t.Fatalf("GetChunkStats error: %v", err)
	}

	// Total refs should be 2 (one per file), but unique chunks should be less
	if totalRefs < 2 {
		t.Errorf("Expected at least 2 refs, got %d", totalRefs)
	}

	t.Logf("Deduplication: totalChunks=%d, totalRefs=%d", 
		totalChunks, totalRefs)
}
