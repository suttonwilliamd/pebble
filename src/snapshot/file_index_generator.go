// File Index Generator
// Purpose: Traverses the repository directory structure and generates an index of all files.

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FileMetadata struct {
	Path         string
	Size         int64
	ModifiedTime time.Time
	IsDirectory  bool
}

type FileIndexGenerator struct {
	RootDir string
}

func NewFileIndexGenerator(rootDir string) *FileIndexGenerator {
	return &FileIndexGenerator{RootDir: rootDir}
}

func (fig *FileIndexGenerator) TraverseDirectory() ([]FileMetadata, error) {
	var fileIndex []FileMetadata

	err := filepath.Walk(fig.RootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			relativePath, err := filepath.Rel(fig.RootDir, path)
			if err != nil {
				return err
			}

			fileMetadata := FileMetadata{
				Path:         relativePath,
				Size:         info.Size(),
				ModifiedTime: info.ModTime(),
				IsDirectory:  false,
			}

			fileIndex = append(fileIndex, fileMetadata)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return fileIndex, nil
}

// shouldSkipDirectory checks if a path should be skipped
func shouldSkipDirectory(path string) bool {
	// Skip any path starting with . (hidden files/directories)
	// Handle both Unix and Windows separators
	cleanPath := filepath.ToSlash(path) // Normalize to forward slashes
	return strings.HasPrefix(cleanPath, ".")
}

// GenerateIndex creates a FileIndex with content hashes for all files
func (fig *FileIndexGenerator) GenerateIndex() ([]FileIndex, error) {
	var indexList []FileIndex

	err := filepath.Walk(fig.RootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(fig.RootDir, path)
		if err != nil {
			return err
		}

		// Skip directories and files in skip directories (but not root)
		if info.IsDir() && shouldSkipDirectory(relativePath) && relativePath != "." {
			return filepath.SkipDir
		}

		if !info.IsDir() && !shouldSkipDirectory(relativePath) {
			// Calculate content hash
			hash, err := fig.calculateFileHash(path)
			if err != nil {
				return err
			}

			indexEntry := FileIndex{
				Path: relativePath,
				Hash: hash,
			}

			indexList = append(indexList, indexEntry)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return indexList, nil
}

// calculateFileHash computes SHA256 hash of file content
func (fig *FileIndexGenerator) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
