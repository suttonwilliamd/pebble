package git

import (
	"fmt"
	"os"
	"os/exec"
)

// GitExporter is responsible for exporting a Pebble repository to Git
type GitExporter struct {
}

// NewGitExporter creates a new GitExporter instance
func NewGitExporter() *GitExporter {
	return &GitExporter{}
}

// ExportGitRepository exports a Pebble repository to Git
func (ge *GitExporter) ExportGitRepository(pebbleRepoPath, gitRepoPath string) error {
	// Check if the Pebble repository exists
	if _, err := os.Stat(pebbleRepoPath); os.IsNotExist(err) {
		return fmt.Errorf("Pebble repository does not exist: %s", pebbleRepoPath)
	}

	// Create the Git repository directory
	err := os.MkdirAll(gitRepoPath, 0755)
	if err != nil {
		return err
	}

	// Initialize the Git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = gitRepoPath
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to initialize Git repository: %v", err)
	}

	// TODO: Convert Pebble snapshots to Git commits
	fmt.Printf("Exported Pebble repository to Git: %s\n", gitRepoPath)

	return nil
}
