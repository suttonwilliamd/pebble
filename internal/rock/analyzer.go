package rock

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileType represents the type of file for chunking decisions
type FileType int

const (
	FileTypeUnknown FileType = iota
	FileTypeText
	FileTypeBinary
	FileTypeImage
	FileTypeVideo
	FileTypeArchive
	FileTypeExecutable
)

// FileAnalyzer analyzes files to determine their type and suitability for chunking
type FileAnalyzer struct {
	// Configuration
	MinFileSize int64 // Minimum file size to consider for chunking
	MaxFileSize int64 // Maximum file size to chunk (larger files use streaming)
}

// NewFileAnalyzer creates a new file analyzer
func NewFileAnalyzer(minSize, maxSize int64) *FileAnalyzer {
	return &FileAnalyzer{
		MinFileSize: minSize,
		MaxFileSize: maxSize,
	}
}

// DefaultFileAnalyzer creates an analyzer with sensible defaults
func DefaultFileAnalyzer() *FileAnalyzer {
	return NewFileAnalyzer(1024, 100*1024*1024) // 1KB min, 100MB max
}

// FileInfo contains information about an analyzed file
type FileInfo struct {
	Path         string
	Size         int64
	FileType     FileType
	SuitableForChunking bool
	ShouldStream        bool // Use streaming for large files
	MimeType     string
	Extension    string
}

// AnalyzeFile analyzes a file and returns its information
func (a *FileAnalyzer) AnalyzeFile(path string) (*FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", path)
	}

	fileInfo := &FileInfo{
		Path:         path,
		Size:         info.Size(),
		SuitableForChunking: true,
		ShouldStream:        info.Size() > a.MaxFileSize,
	}

	// Determine file type from extension
	ext := filepath.Ext(path)
	fileInfo.Extension = ext
	fileInfo.FileType = a.detectFileType(ext, path)
	
	// Check if suitable for chunking
	if info.Size() < a.MinFileSize {
		fileInfo.SuitableForChunking = false
	}
	
	if info.Size() > a.MaxFileSize {
		// Large files - use streaming chunker
		fileInfo.SuitableForChunking = true
		fileInfo.ShouldStream = true
	}

	return fileInfo, nil
}

// detectFileType determines the file type from extension and content
func (a *FileAnalyzer) detectFileType(ext, path string) FileType {
	// Normalize extension
	ext = normalizeExt(ext)

	// Text files - good for chunking but may have poor dedup
	switch ext {
	case ".txt", ".md", ".json", ".xml", ".csv", ".log", ".yaml", ".yml", ".toml",
		".html", ".css", ".js", ".ts", ".go", ".rs", ".py", ".java", ".c", ".cpp",
		".h", ".hpp", ".sh", ".bash", ".ps1", ".sql":
		return FileTypeText
	}

	// Images - good for chunking, good dedup
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".svg", ".ico", ".tiff":
		return FileTypeImage
	}

	// Video - good for chunking, excellent dedup
	switch ext {
	case ".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm":
		return FileTypeVideo
	}

	// Archives - excellent for chunking/dedup
	switch ext {
	case ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar":
		return FileTypeArchive
	}

	// Executables - good for chunking/dedup
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".iso", ".img":
		return FileTypeExecutable
	}

	// Check binary content for unknown extensions
	return FileTypeBinary
}

// normalizeExt normalizes file extension to lowercase
func normalizeExt(ext string) string {
	if len(ext) > 0 && ext[0] == '.' {
		ext = ext[1:]
	}
	return "." + ext
}

// IsBinaryFile checks if a file appears to be binary
func IsBinaryFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	// Check first 8KB for binary content
	checkSize := len(data)
	if checkSize > 8192 {
		checkSize = 8192
	}

	// Binary files tend to have more null bytes and high concentration
	// of non-printable characters
	nullCount := 0
	nonPrintable := 0

	for i := 0; i < checkSize; i++ {
		if data[i] == 0 {
			nullCount++
		} else if data[i] < 32 && data[i] != '\t' && data[i] != '\n' && data[i] != '\r' {
			nonPrintable++
		}
	}

	threshold := checkSize / 10 // 10% threshold
	return nullCount > threshold || nonPrintable > threshold, nil
}

// AnalyzeDirectory analyzes all files in a directory recursively
func (a *FileAnalyzer) AnalyzeDirectory(dirPath string) ([]*FileInfo, error) {
	var results []*FileInfo

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Skip hidden files
		if filepath.Base(path)[0] == '.' {
			return nil
		}

		fileInfo, err := a.AnalyzeFile(path)
		if err != nil {
			// Log but don't fail on individual file errors
			return nil
		}

		results = append(results, fileInfo)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory: %w", err)
	}

	return results, nil
}

// GetSuitableFiles returns only files suitable for chunking
func (a *FileAnalyzer) GetSuitableFiles(infos []*FileInfo) []*FileInfo {
	var suitable []*FileInfo
	for _, info := range infos {
		if info.SuitableForChunking {
			suitable = append(suitable, info)
		}
	}
	return suitable
}
