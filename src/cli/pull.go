package cli

import (
	"fmt"
	"pebble/src/sync"
)

// PullCommand represents the `pebble pull` command
type PullCommand struct {
	SyncProtocol *sync.HTTP2Protocol
}

// NewPullCommand creates a new PullCommand instance
func NewPullCommand(syncProtocol *sync.HTTP2Protocol) *PullCommand {
	return &PullCommand{SyncProtocol: syncProtocol}
}

// Run executes the pull command
func (pc *PullCommand) Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: pebble pull <remote>")
	}

	remote := args[0]

	// TODO: Implement pull
	fmt.Printf("Pulled from remote: %s\n", remote)

	return nil
}
