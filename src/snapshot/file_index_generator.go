// File Index Generator
// Purpose: Traverses the repository directory structure and generates an index of all files.

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
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

// GenerateIndex creates a FileIndex with content hashes for all files
func (fig *FileIndexGenerator) GenerateIndex() ([]FileIndex, error) {
	var fileIndex []FileIndex

	err := filepath.Walk(fig.RootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			relativePath, err := filepath.Rel(fig.RootDir, path)
			if err != nil {
				return err
			}

			// Calculate content hash
			hash, err := fig.calculateFileHash(path)
			if err != nil {
				return err
			}

			fileIndexEntry := FileIndex{
				Path: relativePath,
				Hash: hash,
			}

			fileIndex = append(fileIndex, fileIndexEntry)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return fileIndex, nil
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
