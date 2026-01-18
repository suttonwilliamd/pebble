package cli

import (
	"fmt"
	"pebble/src/snapshot"
	"time"
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

	// Generate a unique root hash for the snapshot
	rootHash := fmt.Sprintf("snapshot-%d", time.Now().UnixNano())

	// Check if there are any files to commit
	if len(files) == 0 {
		return fmt.Errorf("no files to commit")
	}

	// Store snapshot in database
	timestamp := time.Now().Unix()
	snapshotID, err := cc.ObjectDatabase.InsertSnapshot(timestamp, rootHash)
	if err != nil {
		return fmt.Errorf("failed to store snapshot: %v", err)
	}

	// Store files in snapshot
	for _, file := range files {
		err := cc.ObjectDatabase.InsertSnapshotObject(snapshotID, file.Hash, file.Path)
		if err != nil {
			return fmt.Errorf("failed to store snapshot object: %v", err)
		}
	}

	fmt.Printf("Committed snapshot %d: %s\n", snapshotID, message)
	return nil
}
