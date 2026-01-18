package cli

import (
	"fmt"
	"os"
	"os/exec"
	"pebble/src/snapshot"
)

// InitCommand represents the `pebble init` command
type InitCommand struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

// NewInitCommand creates a new InitCommand instance
func NewInitCommand(objectDatabase *snapshot.ObjectDatabase) *InitCommand {
	return &InitCommand{ObjectDatabase: objectDatabase}
}

// Run executes the init command
func (ic *InitCommand) Run(args []string) error {
	// Check if already initialized
	if _, err := os.Stat(".pebble"); err == nil {
		return fmt.Errorf("repository already initialized")
	}

	// Create .pebble directory
	if err := os.MkdirAll(".pebble", 0755); err != nil {
		return fmt.Errorf("failed to create .pebble directory: %v", err)
	}

	// Initialize object database
	odb, err := snapshot.NewObjectDatabase(".pebble/objects.db")
	if err != nil {
		return fmt.Errorf("failed to initialize object database: %v", err)
	}
	defer odb.DB.Close()

	fmt.Println("Initialized empty pebble repository in .pebble/")
	return nil
}

// TestCommand represents the `pebble test` command
type TestCommand struct {
}

// NewTestCommand creates a new TestCommand instance
func NewTestCommand() *TestCommand {
	return &TestCommand{}
}

// Run executes the test command
func (tc *TestCommand) Run(args []string) error {
	fmt.Println("Running test suite...")
	cmd := exec.Command("go", "test", "./src/test_suite/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running tests: %v", err)
	}
	return nil
}

// HelpCommand represents the `pebble help` command
type HelpCommand struct {
}

// NewHelpCommand creates a new HelpCommand instance
func NewHelpCommand() *HelpCommand {
	return &HelpCommand{}
}

// Run executes the help command
func (hc *HelpCommand) Run(args []string) error {
	ShowHelp()
	return nil
}
