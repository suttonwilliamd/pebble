// Content Addressing
// Purpose: Generates SHA-256 hashes for file contents to ensure data integrity and enable deduplication.

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

type ContentAddressing struct {
}

func NewContentAddressing() *ContentAddressing {
	return &ContentAddressing{}
}

func (ca *ContentAddressing) GenerateHash(filePath string) (string, error) {
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