package cli

import (
	"fmt"
	"pebble/src/snapshot"
)

// CheckoutCommand represents the `pebble checkout` command
type CheckoutCommand struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

// NewCheckoutCommand creates a new CheckoutCommand instance
func NewCheckoutCommand(objectDatabase *snapshot.ObjectDatabase) *CheckoutCommand {
	return &CheckoutCommand{ObjectDatabase: objectDatabase}
}

// Run executes the checkout command
func (cc *CheckoutCommand) Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: pebble checkout <branch>")
	}

	branch := args[0]

	// TODO: Implement checkout
	fmt.Printf("Checked out branch: %s\n", branch)

	return nil
}
