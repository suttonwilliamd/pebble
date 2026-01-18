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
	// TODO: Implement log command
	fmt.Println("Commit history:")
	fmt.Println("  - Initial commit")

	return nil
}
