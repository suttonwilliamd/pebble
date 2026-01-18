// File Comparator
// Purpose: Compares individual files to identify conflicts.

package combine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

type FileConflict struct {
	FilePath string
	Type     string // "added", "modified", "deleted"
}

type FileComparator struct {
}

func NewFileComparator() *FileComparator {
	return &FileComparator{}
}

func (fc *FileComparator) CompareFiles(filePath1, filePath2 string) (bool, error) {
	// Generate hashes for both files
	hash1, err := fc.generateFileHash(filePath1)
	if err != nil {
		return false, err
	}

	hash2, err := fc.generateFileHash(filePath2)
	if err != nil {
		return false, err
	}

	// Compare hashes
	return hash1 == hash2, nil
}

func (fc *FileComparator) generateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	hashBytes := hash.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)

	return hashString, nil
}