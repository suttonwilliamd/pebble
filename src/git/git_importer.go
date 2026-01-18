package git

import (
	"fmt"
	"os"
	"os/exec"
)

// GitImporter is responsible for importing a Git repository into Pebble
type GitImporter struct {
}

// NewGitImporter creates a new GitImporter instance
func NewGitImporter() *GitImporter {
	return &GitImporter{}
}

// ImportGitRepository imports a Git repository into Pebble
func (gi *GitImporter) ImportGitRepository(gitRepoPath, pebbleRepoPath string) error {
	// Check if the Git repository exists
	if _, err := os.Stat(gitRepoPath); os.IsNotExist(err) {
		return fmt.Errorf("Git repository does not exist: %s", gitRepoPath)
	}

	// Create the Pebble repository directory
	err := os.MkdirAll(pebbleRepoPath, 0755)
	if err != nil {
		return err
	}

	// Run git log to get the commit history
	cmd := exec.Command("git", "log", "--pretty=format:%H %s", "--reverse")
	cmd.Dir = gitRepoPath
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to get Git commit history: %v", err)
	}

	// TODO: Parse the Git commit history and convert to Pebble snapshots
	fmt.Printf("Imported Git repository: %s\n", gitRepoPath)

	return nil
}
