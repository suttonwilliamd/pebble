package rock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileAnalyzer_DetectFileType(t *testing.T) {
	analyzer := DefaultFileAnalyzer()

	tests := []struct {
		ext      string
		expected FileType
	}{
		{".go", FileTypeText},
		{".txt", FileTypeText},
		{".json", FileTypeText},
		{".png", FileTypeImage},
		{".jpg", FileTypeImage},
		{".mp4", FileTypeVideo},
		{".zip", FileTypeArchive},
		{".exe", FileTypeExecutable},
		{".dll", FileTypeExecutable},
		{".bin", FileTypeExecutable},
	}

	for _, tt := range tests {
		info, err := analyzer.analyzeWithExt(tt.ext)
		if err != nil {
			t.Errorf("analyzeWithExt(%s) error: %v", tt.ext, err)
			continue
		}
		if info.FileType != tt.expected {
			t.Errorf("analyzeWithExt(%s) = %v, want %v", tt.ext, info.FileType, tt.expected)
		}
	}
}

// analyzeWithExt is a helper for testing without actual files
func (a *FileAnalyzer) analyzeWithExt(ext string) (*FileInfo, error) {
	return &FileInfo{
		Path:               "test" + ext,
		Size:               1000,
		FileType:           a.detectFileType(ext, "test"+ext),
		SuitableForChunking: true,
	}, nil
}

func TestFileAnalyzer_SizeFiltering(t *testing.T) {
	// Create analyzer with specific thresholds
	analyzer := NewFileAnalyzer(1024, 10000)

	// Test file smaller than min
	info := &FileInfo{Size: 500, SuitableForChunking: true}
	analyzer.applySizeFilter(info)
	if info.SuitableForChunking {
		t.Error("Small file should not be suitable for chunking")
	}

	// Test file in valid range
	info = &FileInfo{Size: 5000, SuitableForChunking: true}
	analyzer.applySizeFilter(info)
	if !info.SuitableForChunking {
		t.Error("Normal file should be suitable for chunking")
	}

	// Test large file that needs streaming
	info = &FileInfo{Size: 50000, SuitableForChunking: true}
	analyzer.applySizeFilter(info)
	if !info.ShouldStream {
		t.Error("Large file should use streaming")
	}
}

func (a *FileAnalyzer) applySizeFilter(info *FileInfo) {
	if info.Size < a.MinFileSize {
		info.SuitableForChunking = false
	}
	if info.Size > a.MaxFileSize {
		info.ShouldStream = true
	}
}

func TestFileAnalyzer_AnalyzeFile(t *testing.T) {
	// Create a temp file to analyze
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	
	if err := os.WriteFile(testFile, []byte("Hello, World!"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	analyzer := DefaultFileAnalyzer()
	info, err := analyzer.AnalyzeFile(testFile)
	if err != nil {
		t.Fatalf("AnalyzeFile error: %v", err)
	}

	if info.Size != 13 {
		t.Errorf("Size = %d, want 13", info.Size)
	}

	if info.FileType != FileTypeText {
		t.Errorf("FileType = %v, want FileTypeText", info.FileType)
	}
}

func TestNormalizeExt(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{".txt", ".txt"},
		{"txt", ".txt"},
		{".go", ".go"},
		{"go", ".go"},
		{"", "."},
	}

	for _, tt := range tests {
		result := normalizeExt(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeExt(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
