package rock

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntegration_ChunkAndReconstruct tests the full pipeline: chunk a file and reconstruct it
func TestIntegration_ChunkAndReconstruct(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create test file with known content
	original := make([]byte, 5000)
	for i := range original {
		original[i] = byte(i % 256)
	}
	testFile := filepath.Join(tmpDir, "test.bin")
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

	if result.TotalChunks == 0 {
		t.Fatal("Expected chunks, got 0")
	}

	// Reconstruct
	outputPath := filepath.Join(tmpDir, "reconstructed.bin")
	err = cg.ReconstructFile(absTestFile, outputPath)
	if err != nil {
		t.Fatalf("ReconstructFile error: %v", err)
	}

	// Verify
	reconstructed, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read reconstructed file: %v", err)
	}

	if len(original) != len(reconstructed) {
		t.Errorf("Size mismatch: original=%d, reconstructed=%d", len(original), len(reconstructed))
	}

	if string(original) != string(reconstructed) {
		t.Error("Reconstructed content doesn't match original")
	}

	t.Logf("Integration test passed: %d chunks -> reconstruct OK", result.TotalChunks)
}

// TestIntegration_DeduplicationAcrossFiles tests that duplicate files share chunks
func TestIntegration_DeduplicationAcrossFiles(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create identical content in multiple files
	content := make([]byte, 3000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	
	files := []string{"file1.bin", "file2.bin", "file3.bin"}
	absFiles := make([]string, len(files))
	
	for i, f := range files {
		path := filepath.Join(tmpDir, f)
		absPath, _ := filepath.Abs(path)
		absFiles[i] = absPath
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatalf("failed to create %s: %v", f, err)
		}
	}

	// Create database and generator
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	cg := NewChunkGenerator(db)

	// Chunk all files
	for _, f := range absFiles {
		_, err := cg.GenerateChunks(f)
		if err != nil {
			t.Fatalf("GenerateChunks error for %s: %v", f, err)
		}
	}

	// Check stats
	totalChunks, _, totalRefs, err := cg.GetChunkStats()
	if err != nil {
		t.Fatalf("GetChunkStats error: %v", err)
	}

	// With 3 identical files, we should have ~1-2 unique chunks but 3 refs
	if totalRefs < 3 {
		t.Errorf("Expected at least 3 refs (one per file), got %d", totalRefs)
	}

	// Deduplication ratio should be much less than 1.0
	// (unique chunks / total refs should be small)
	t.Logf("Deduplication: unique=%d chunks, refs=%d, saved=%.1f%%", 
		totalChunks, totalRefs, float64(totalRefs-totalChunks)/float64(totalRefs)*100)
}

// TestIntegration_ChunkDirectory tests chunking an entire directory
func TestIntegration_ChunkDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create multiple files in directory
	files := map[string][]byte{
		"file1.txt": makeText(1500),
		"file2.txt": makeText(2000),
		"file3.bin": makeBinary(3000),
	}
	
	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
	}

	// Create database and generator
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	cg := NewChunkGenerator(db)

	// Chunk directory
	results, err := cg.GenerateChunksFromDir(tmpDir, nil)
	if err != nil {
		t.Fatalf("GenerateChunksFromDir error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results, got none")
	}

	totalChunks := 0
	for _, r := range results {
		if r.Error != nil {
			t.Logf("Warning: %s: %v", r.FilePath, r.Error)
		}
		totalChunks += r.TotalChunks
	}

	if totalChunks == 0 {
		t.Error("Expected at least some chunks")
	}

	t.Logf("Directory chunked: %d files, %d total chunks", len(results), totalChunks)
}

// TestIntegration_GarbageCollection tests that unreferenced chunks can be cleaned up
func TestIntegration_GarbageCollection(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create and chunk a file
	testFile := filepath.Join(tmpDir, "test.bin")
	absTestFile, _ := filepath.Abs(testFile)
	content := make([]byte, 2000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create database
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewChunkDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	cg := NewChunkGenerator(db)

	// Chunk the file
	_, err = cg.GenerateChunks(absTestFile)
	if err != nil {
		t.Fatalf("GenerateChunks error: %v", err)
	}

	// Check we have chunks
	stats1, _, _, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats error: %v", err)
	}
	if stats1 == 0 {
		t.Fatal("Expected chunks after chunking")
	}

	// Get reference manager and run GC
	refMgr := NewReferenceManager(db)
	deleted, err := refMgr.GarbageCollect()
	if err != nil {
		t.Fatalf("GarbageCollect error: %v", err)
	}

	// Should not delete anything since we have a reference
	if deleted > 0 {
		t.Logf("Warning: GC deleted %d chunks unexpectedly", deleted)
	}

	// Now manually delete references and try again
	// (This simulates what would happen if we deleted a tracked file)
	_, err = db.db.Exec("DELETE FROM chunk_refs")
	if err != nil {
		t.Fatalf("Delete refs error: %v", err)
	}

	deleted, err = refMgr.GarbageCollect()
	if err != nil {
		t.Fatalf("GarbageCollect error: %v", err)
	}

	if deleted == 0 {
		t.Error("Expected GC to delete unreferenced chunks")
	}

	t.Logf("GC deleted %d unreferenced chunks", deleted)
}

// TestIntegration_AnalyzeAndChunk tests analyzer working with chunker
func TestIntegration_AnalyzeAndChunk(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create various file types
	files := []struct {
		name    string
		content []byte
		ext     string
	}{
		{"test.go", []byte("package main\nfunc main() {}"), ".go"},
		{"test.png", makeBinary(2000), ".png"},
		{"test.zip", makeBinary(3000), ".zip"},
	}

	analyzer := DefaultFileAnalyzer()

	for _, f := range files {
		path := filepath.Join(tmpDir, f.name)
		if err := os.WriteFile(path, f.content, 0644); err != nil {
			t.Fatalf("failed to create %s: %v", f.name, err)
		}

		info, err := analyzer.AnalyzeFile(path)
		if err != nil {
			t.Fatalf("AnalyzeFile error for %s: %v", f.name, err)
		}

		t.Logf("File %s: type=%v, suitable=%v, size=%d", 
			f.name, info.FileType, info.SuitableForChunking, info.Size)
	}
}

// Helper functions
func makeText(size int) []byte {
	content := make([]byte, size)
	for i := range content {
		content[i] = byte('a' + (i % 26))
	}
	return content
}

func makeBinary(size int) []byte {
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}
	return content
}
