package cli

import (
	"fmt"
	"pebble/src/snapshot"
)

// LogCommand represents the `pebble log` command
type LogCommand struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

// NewLogCommand creates a new LogCommand instance
func NewLogCommand(objectDatabase *snapshot.ObjectDatabase) *LogCommand {
	return &LogCommand{ObjectDatabase: objectDatabase}
}

// Run executes the log command
func (lc *LogCommand) Run(args []string) error {
	if lc.ObjectDatabase == nil {
		return fmt.Errorf("object database not initialized")
	}

	// Get all snapshots from database
	snapshots, err := lc.ObjectDatabase.GetAllSnapshots()
	if err != nil {
		return fmt.Errorf("failed to retrieve snapshots: %v", err)
	}

	if len(snapshots) == 0 {
		fmt.Println("No commits found.")
		return nil
	}

	fmt.Println("Commit history:")
	for _, snapshot := range snapshots {
		fmt.Printf("  - %s\n", snapshot.RootHash)
	}

	return nil
}
