package cli

import (
	"fmt"
	"os"
	"pebble/src/snapshot"
)

var commands map[string]*Command

func init() {
	commands = make(map[string]*Command)

	// Initialize object database (shared across commands)
	odb, err := snapshot.NewObjectDatabase(".pebble/objects.db")
	if err != nil {
		// Don't fail if database doesn't exist yet (for commands like init)
		odb = nil
	}

	// Register commands
	registerCommand("init", "Initialize a new pebble repository", NewInitCommand(odb))
	registerCommand("status", "Show repository status", NewStatusCommand(odb))
	registerCommand("commit", "Create a new snapshot", NewCommitCommand(odb))
	registerCommand("log", "Show commit history", NewLogCommand(odb))
	// For now, pass nil to push/pull - they will be fixed later
	registerCommand("push", "Push changes to remote", NewPushCommand(nil))
	registerCommand("pull", "Pull changes from remote", NewPullCommand(nil))
	registerCommand("branch", "Manage branches", NewBranchCommand(odb))
	registerCommand("checkout", "Switch branches", NewCheckoutCommand(odb))
	registerCommand("test", "Run the test suite", NewTestCommand())
	registerCommand("help", "Show help information", NewHelpCommand())
}

func registerCommand(name, description string, cmd interface{}) {
	// Convert any command type to our Command interface
	var runFunc func([]string) error

	switch c := cmd.(type) {
	case *InitCommand:
		runFunc = c.Run
	case *StatusCommand:
		runFunc = c.Run
	case *CommitCommand:
		runFunc = c.Run
	case *LogCommand:
		runFunc = c.Run
	case *PushCommand:
		runFunc = c.Run
	case *PullCommand:
		runFunc = c.Run
	case *BranchCommand:
		runFunc = c.Run
	case *CheckoutCommand:
		runFunc = c.Run
	case *TestCommand:
		runFunc = c.Run
	case *HelpCommand:
		runFunc = c.Run
	default:
		return
	}

	commands[name] = NewCommand(name, description, runFunc)
}

func Main() {
	if len(os.Args) < 2 {
		ShowHelp()
		return
	}

	commandName := os.Args[1]
	args := os.Args[2:]

	cmd, exists := commands[commandName]
	if !exists {
		fmt.Printf("Unknown command: %s\n", commandName)
		fmt.Println("Use 'pebble help' for usage information.")
		os.Exit(1)
	}

	if err := cmd.Execute(args); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func ShowHelp() {
	fmt.Println("Pebble CLI - Version Control System")
	fmt.Println("====================================")
	fmt.Println()
	fmt.Println("Usage: pebble [command] [options]")
	fmt.Println()
	fmt.Println("Available commands:")
	for name, cmd := range commands {
		fmt.Printf("  %-12s %s\n", name, cmd.Description)
	}
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  pebble init")
	fmt.Println("  pebble status")
	fmt.Println("  pebble commit \"Initial commit\"")
	fmt.Println("  pebble help")
}
