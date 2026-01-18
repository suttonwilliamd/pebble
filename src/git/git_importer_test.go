package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitImporter(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_git_importer")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test Git repository
	gitRepoPath := filepath.Join(tmpDir, "test_git_repo")
	err = os.MkdirAll(gitRepoPath, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize the Git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = gitRepoPath
	err = cmd.Run()
	if err != nil {
		t.Fatal(err)
	}

	// Create a test file
	testFile := filepath.Join(gitRepoPath, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Commit the file
	cmd = exec.Command("git", "add", "test.txt")
	cmd.Dir = gitRepoPath
	err = cmd.Run()
	if err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = gitRepoPath
	err = cmd.Run()
	if err != nil {
		t.Fatal(err)
	}

	// Import the Git repository
	pebbleRepoPath := filepath.Join(tmpDir, "test_pebble_repo")
	gi := NewGitImporter()
	err = gi.ImportGitRepository(gitRepoPath, pebbleRepoPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the Pebble repository was created
	if _, err := os.Stat(pebbleRepoPath); os.IsNotExist(err) {
		t.Error("Pebble repository was not created")
	}
}
