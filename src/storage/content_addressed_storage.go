package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// ContentAddressedStorage is responsible for storing objects using content-addressed filenames
type ContentAddressedStorage struct {
	RootPath string
}

// NewContentAddressedStorage creates a new ContentAddressedStorage instance
func NewContentAddressedStorage(rootPath string) *ContentAddressedStorage {
	return &ContentAddressedStorage{RootPath: rootPath}
}

// StoreObject stores an object in the content-addressed storage
func (cas *ContentAddressedStorage) StoreObject(data []byte) (string, error) {
	// Generate hash for the object
	hash := sha256.New()
	hash.Write(data)
	objectHash := hex.EncodeToString(hash.Sum(nil))

	// Create the directory structure
	objectPath := filepath.Join(cas.RootPath, objectHash[:2], objectHash[2:4])
	err := os.MkdirAll(objectPath, 0755)
	if err != nil {
		return "", err
	}

	// Write the object to the file
	objectFilePath := filepath.Join(objectPath, objectHash[4:])
	err = os.WriteFile(objectFilePath, data, 0644)
	if err != nil {
		return "", err
	}

	return objectHash, nil
}

// RetrieveObject retrieves an object from the content-addressed storage
func (cas *ContentAddressedStorage) RetrieveObject(hash string) ([]byte, error) {
	// Construct the object path
	objectPath := filepath.Join(cas.RootPath, hash[:2], hash[2:4], hash[4:])

	// Read the object from the file
	return os.ReadFile(objectPath)
}

// DeleteObject deletes an object from the content-addressed storage
func (cas *ContentAddressedStorage) DeleteObject(hash string) error {
	// Construct the object path
	objectPath := filepath.Join(cas.RootPath, hash[:2], hash[2:4], hash[4:])

	// Delete the object file
	return os.Remove(objectPath)
}
