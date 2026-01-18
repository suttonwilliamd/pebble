package cli

import (
	"fmt"
	"pebble/src/sync"
)

// PushCommand represents the `pebble push` command
type PushCommand struct {
	SyncProtocol *sync.HTTP2Protocol
}

// NewPushCommand creates a new PushCommand instance
func NewPushCommand(syncProtocol *sync.HTTP2Protocol) *PushCommand {
	return &PushCommand{SyncProtocol: syncProtocol}
}

// Run executes the push command
func (pc *PushCommand) Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: pebble push <remote>")
	}

	remote := args[0]

	// TODO: Implement push
	fmt.Printf("Pushed to remote: %s\n", remote)

	return nil
}
