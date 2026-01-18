package cli

import (
	"fmt"
	"pebble/src/snapshot"
)

// StatusCommand represents the `pebble status` command
type StatusCommand struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

// NewStatusCommand creates a new StatusCommand instance
func NewStatusCommand(objectDatabase *snapshot.ObjectDatabase) *StatusCommand {
	return &StatusCommand{ObjectDatabase: objectDatabase}
}

// Run executes the status command
func (sc *StatusCommand) Run(args []string) error {
	// Generate file index
	fig := snapshot.NewFileIndexGenerator(".")
	files, err := fig.GenerateIndex()
	if err != nil {
		return err
	}

	// Generate content addresses
	ca := snapshot.NewContentAddressing()
	for i := range files {
		hash, err := ca.GenerateHash(files[i].Path)
		if err != nil {
			return err
		}
		files[i].Hash = hash
	}

	// Display status
	fmt.Println("Repository status:")
	for _, file := range files {
		fmt.Printf("  - %s (hash: %s)\n", file.Path, file.Hash)
	}

	return nil
}
