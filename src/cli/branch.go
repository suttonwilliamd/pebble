package cli

import (
	"fmt"
	"pebble/src/snapshot"
)

// BranchCommand represents the `pebble branch` command
type BranchCommand struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

// NewBranchCommand creates a new BranchCommand instance
func NewBranchCommand(objectDatabase *snapshot.ObjectDatabase) *BranchCommand {
	return &BranchCommand{ObjectDatabase: objectDatabase}
}

// Run executes the branch command
func (bc *BranchCommand) Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: pebble branch <name>")
	}

	name := args[0]

	// TODO: Implement branch creation
	fmt.Printf("Created branch: %s\n", name)

	return nil
}
