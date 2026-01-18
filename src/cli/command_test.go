package cli

import (
	"testing"
)

func TestCommand(t *testing.T) {
	// Create a test command
	command := NewCommand("test", "Test command", func(args []string) error {
		return nil
	})

	// Verify command properties
	if command.Name != "test" {
		t.Errorf("Expected command name 'test', got '%s'", command.Name)
	}

	if command.Description != "Test command" {
		t.Errorf("Expected command description 'Test command', got '%s'", command.Description)
	}

	// Execute the command
	err := command.Execute([]string{})
	if err != nil {
		t.Errorf("Command execution failed: %v", err)
	}
}
