package combine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestMaterializeConflict tests creating conflict files
func TestMaterializeConflict(t *testing.T) {
	tmpDir := t.TempDir()

	conflict := FileConflict{
		Path:         "test.txt",
		ConflictType: ConflictTypeBothModified,
		OurContent:   []byte("our changes"),
		TheirContent: []byte("their changes"),
	}

	err := MaterializeConflict(conflict, tmpDir)
	if err != nil {
		t.Fatalf("MaterializeConflict error: %v", err)
	}

	// Check file exists
	conflictPath := filepath.Join(tmpDir, ".pebble", "conflicts", "test.txt.conflict")
	content, err := os.ReadFile(conflictPath)
	if err != nil {
		t.Fatalf("Failed to read conflict file: %v", err)
	}

	// Check markers are present
	contentStr := string(content)
	if !bytes.Contains([]byte(contentStr), []byte("<<<<<<< OURS")) {
		t.Error("Missing OURS marker")
	}
	if !bytes.Contains([]byte(contentStr), []byte("=======")) {
		t.Error("Missing separator")
	}
	if !bytes.Contains([]byte(contentStr), []byte(">>>>>>> THEIRS")) {
		t.Error("Missing THEIRS marker")
	}
	if !bytes.Contains([]byte(contentStr), []byte("our changes")) {
		t.Error("Missing our content")
	}
	if !bytes.Contains([]byte(contentStr), []byte("their changes")) {
		t.Error("Missing their content")
	}
}

// TestMaterializeConflict_NestedPath tests nested path conflict files
func TestMaterializeConflict_NestedPath(t *testing.T) {
	tmpDir := t.TempDir()

	conflict := FileConflict{
		Path:         "subdir/nested/test.txt",
		ConflictType: ConflictTypeBothModified,
		OurContent:   []byte("our nested"),
		TheirContent: []byte("their nested"),
	}

	err := MaterializeConflict(conflict, tmpDir)
	if err != nil {
		t.Fatalf("MaterializeConflict error: %v", err)
	}

	// Check file exists
	conflictPath := filepath.Join(tmpDir, ".pebble", "conflicts", "subdir", "nested", "test.txt.conflict")
	_, err = os.ReadFile(conflictPath)
	if err != nil {
		t.Fatalf("Failed to read conflict file: %v", err)
	}
}

// TestApplyResolution_Write tests writing resolved content
func TestApplyResolution_Write(t *testing.T) {
	tmpDir := t.TempDir()

	res := Resolution{
		Path:    "resolved.txt",
		Content: []byte("resolved content"),
		Action:  "ours",
	}

	err := ApplyResolution(res, tmpDir)
	if err != nil {
		t.Fatalf("ApplyResolution error: %v", err)
	}

	// Check file exists
	filePath := filepath.Join(tmpDir, "resolved.txt")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read resolved file: %v", err)
	}

	if string(content) != "resolved content" {
		t.Errorf("Content = %v, want 'resolved content'", string(content))
	}
}

// TestApplyResolution_Delete tests deleting file on resolution
func TestApplyResolution_Delete(t *testing.T) {
	tmpDir := t.TempDir()

	// Create file first
	testFile := filepath.Join(tmpDir, "to-delete.txt")
	if err := os.WriteFile(testFile, []byte("delete me"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	res := Resolution{
		Path:   "to-delete.txt",
		Action: "delete",
	}

	err := ApplyResolution(res, tmpDir)
	if err != nil {
		t.Fatalf("ApplyResolution error: %v", err)
	}

	// Check file is deleted
	_, err = os.ReadFile(testFile)
	if !os.IsNotExist(err) {
		t.Errorf("Expected file to be deleted, but got error: %v", err)
	}
}

// TestApplyResolution_NestedPath tests writing to nested path
func TestApplyResolution_NestedPath(t *testing.T) {
	tmpDir := t.TempDir()

	res := Resolution{
		Path:    "subdir/deep/path.txt",
		Content: []byte("nested content"),
		Action:  "theirs",
	}

	err := ApplyResolution(res, tmpDir)
	if err != nil {
		t.Fatalf("ApplyResolution error: %v", err)
	}

	// Check file exists
	filePath := filepath.Join(tmpDir, "subdir", "deep", "path.txt")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read resolved file: %v", err)
	}

	if string(content) != "nested content" {
		t.Errorf("Content = %v, want 'nested content'", string(content))
	}
}
