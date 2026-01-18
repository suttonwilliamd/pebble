package cli

import (
	"fmt"
	"pebble/src/snapshot"
)

// CommitCommand represents the `pebble commit` command
type CommitCommand struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

// NewCommitCommand creates a new CommitCommand instance
func NewCommitCommand(objectDatabase *snapshot.ObjectDatabase) *CommitCommand {
	return &CommitCommand{ObjectDatabase: objectDatabase}
}

// Run executes the commit command
func (cc *CommitCommand) Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: pebble commit <message>")
	}

	message := args[0]

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

	// Generate metadata
	mg := snapshot.NewMetadataGenerator()
	var metadata []snapshot.Metadata
	for _, file := range files {
		metadataItem, err := mg.GenerateMetadataForFile(file.Path, file.Hash)
		if err != nil {
			return err
		}
		metadata = append(metadata, metadataItem)
	}

	// Store snapshot
	// TODO: Implement snapshot storage
	fmt.Printf("Committed snapshot with message: %s\n", message)

	fmt.Printf("Committed snapshot with message: %s\n", message)
	return nil
}
