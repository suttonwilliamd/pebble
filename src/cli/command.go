package cli

// Command represents a CLI command
type Command struct {
	Name        string
	Description string
	Run         func([]string) error
}

// NewCommand creates a new Command instance
func NewCommand(name, description string, run func([]string) error) *Command {
	return &Command{
		Name:        name,
		Description: description,
		Run:         run,
	}
}

// Execute runs the command with the given arguments
func (c *Command) Execute(args []string) error {
	return c.Run(args)
}
