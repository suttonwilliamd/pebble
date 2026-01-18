package storage

import (
	"os"
	"testing"
)

func TestContentAddressedStorage(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_content_addressed_storage")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create ContentAddressedStorage instance
	cas := NewContentAddressedStorage(tmpDir)

	// Test data
	testData := []byte("test data")

	// Store object
	hash, err := cas.StoreObject(testData)
	if err != nil {
		t.Fatal(err)
	}

	// Verify hash is generated
	if hash == "" {
		t.Error("Hash not generated")
	}

	// Retrieve object
	retrievedData, err := cas.RetrieveObject(hash)
	if err != nil {
		t.Fatal(err)
	}

	// Verify retrieved data
	if string(retrievedData) != string(testData) {
		t.Errorf("Retrieved data does not match stored data")
	}

	// Delete object
	err = cas.DeleteObject(hash)
	if err != nil {
		t.Fatal(err)
	}

	// Verify object is deleted
	_, err = cas.RetrieveObject(hash)
	if err == nil {
		t.Error("Object not deleted")
	}
}
