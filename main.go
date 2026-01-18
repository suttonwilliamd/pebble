package main

import (
	"fmt"
	"os"
	"pebble/src/cli"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Pebble CLI - Version Control System")
		fmt.Println("====================================")
		fmt.Println()
		fmt.Println("Usage: pebble [command] [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  test        Run the test suite")
		fmt.Println("  help        Show this help screen")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  pebble test")
		fmt.Println("  pebble help")
		fmt.Println()
		fmt.Println("For more information, visit the documentation.")
		return
	}

	cli.Main()
}
