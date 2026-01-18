// User Interface
// Purpose: Provides a user interface for resolving conflicts.

package combine

import (
	"fmt"
	"os"
)

type UserInterface struct {
}

func NewUserInterface() *UserInterface {
	return &UserInterface{}
}

func (ui *UserInterface) ResolveConflicts(conflicts []FileConflict) error {
	for _, conflict := range conflicts {
		fmt.Printf("Conflict detected in file: %s (Type: %s)\n", conflict.FilePath, conflict.Type)
		fmt.Printf("Choose an option:\n")
		fmt.Printf("1. Keep local version\n")
		fmt.Printf("2. Keep remote version\n")
		fmt.Printf("3. Merge manually\n")
		fmt.Printf("Enter your choice (1-3): ")

		var choice int
		if _, err := fmt.Scanln(&choice); err != nil {
			return err
		}

		switch choice {
		case 1:
			// Keep local version
			fmt.Printf("Keeping local version of %s\n", conflict.FilePath)
		case 2:
			// Keep remote version
			fmt.Printf("Keeping remote version of %s\n", conflict.FilePath)
			if err := ui.copyFile(conflict.FilePath+".theirs", conflict.FilePath); err != nil {
				return err
			}
		case 3:
			// Merge manually
			fmt.Printf("Please merge %s and %s.theirs manually\n", conflict.FilePath, conflict.FilePath)
		default:
			fmt.Printf("Invalid choice. Keeping local version of %s\n", conflict.FilePath)
		}
	}

	return nil
}

func (ui *UserInterface) copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	err = os.WriteFile(dst, input, 0644)
	return err
}