// File Analyzer
// Purpose: Analyzes binary files to determine if they are suitable for chunking.

package rock

import (
	"os"
)

type FileAnalysis struct {
	Path       string
	Size       int64
	Type       string
	IsSuitable bool
}

type FileAnalyzer struct {
}

func NewFileAnalyzer() *FileAnalyzer {
	return &FileAnalyzer{}
}

func (fa *FileAnalyzer) AnalyzeFile(filePath string) (*FileAnalysis, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	// Determine file type (simplified for example)
	fileType := "application/octet-stream"
	if fileInfo.IsDir() {
		fileType = "directory"
	} else {
		// Check file extension for common binary types
		if ext := filepath.Ext(filePath); ext != "" {
			switch ext {
			case ".png", ".jpg", ".jpeg", ".gif":
				fileType = "image/" + ext[1:]
			case ".pdf":
				fileType = "application/pdf"
			case ".zip", ".tar", ".gz":
				fileType = "application/" + ext[1:]
			}
		}
	}

	// Determine if the file is suitable for chunking
	isSuitable := !fileInfo.IsDir() && (fileType == "application/octet-stream" || fileType == "application/pdf" || fileType == "application/zip")

	return &FileAnalysis{
		Path:       filePath,
		Size:       fileInfo.Size(),
		Type:       fileType,
		IsSuitable: isSuitable,
	}, nil
}