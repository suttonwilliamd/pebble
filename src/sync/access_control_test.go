package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessControl(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_access_control")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create AccessControl instance
	dbPath := filepath.Join(tmpDir, "access_control.db")
	ac, err := NewAccessControl(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ac.Close()

	// Test adding a user
	err = ac.AddUser("testuser", "testpassword", "user")
	if err != nil {
		t.Fatal(err)
	}

	// Test authenticating a user
	authenticated, err := ac.AuthenticateUser("testuser", "testpassword")
	if err != nil {
		t.Fatal(err)
	}

	if !authenticated {
		t.Error("User authentication failed")
	}

	// Test adding a permission
	err = ac.AddPermission("user", "repository", "read")
	if err != nil {
		t.Fatal(err)
	}

	// Test authorizing a user
	authorized, err := ac.AuthorizeUser("testuser", "repository", "read")
	if err != nil {
		t.Fatal(err)
	}

	if !authorized {
		t.Error("User authorization failed")
	}

	// Test denying access to unauthorized users
	authorized, err = ac.AuthorizeUser("testuser", "repository", "write")
	if err != nil {
		t.Fatal(err)
	}

	if authorized {
		t.Error("User authorization should have failed")
	}
}
