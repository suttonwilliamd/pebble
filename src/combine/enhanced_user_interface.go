package combine

import (
	"fmt"
	"os"
)

// EnhancedUserInterface is responsible for providing an enhanced user interface for resolving conflicts
type EnhancedUserInterface struct {
}

// NewEnhancedUserInterface creates a new EnhancedUserInterface instance
func NewEnhancedUserInterface() *EnhancedUserInterface {
	return &EnhancedUserInterface{}
}

// DisplayConflicts displays conflicts to the user with resolution options
func (eui *EnhancedUserInterface) DisplayConflicts(conflicts []Conflict) {
	fmt.Println("Conflicts detected:")
	for i, conflict := range conflicts {
		fmt.Printf("%d. %s (%s)\n", i+1, conflict.Path, conflict.Type)
	}
}

// ResolveConflict prompts the user to resolve a conflict with enhanced options
func (eui *EnhancedUserInterface) ResolveConflict(conflict Conflict) (string, error) {
	fmt.Printf("Resolve conflict for %s:\n", conflict.Path)
	fmt.Println("1. Use theirs")
	fmt.Println("2. Use ours")
	fmt.Println("3. Manual resolution")
	fmt.Println("4. View diff")

	var choice int
	_, err := fmt.Scan(&choice)
	if err != nil {
		return "", err
	}

	switch choice {
	case 1:
		return "theirs", nil
	case 2:
		return "ours", nil
	case 3:
		return "manual", nil
	case 4:
		return "diff", nil
	default:
		return "", fmt.Errorf("invalid choice")
	}
}

// LogResolution logs the resolution of a conflict with additional details
func (eui *EnhancedUserInterface) LogResolution(conflict Conflict, resolution string, details string) error {
	logFile, err := os.OpenFile("conflict_resolutions.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	logEntry := fmt.Sprintf("%s: %s resolved as %s (details: %s)\n", conflict.Path, conflict.Type, resolution, details)
	_, err = logFile.WriteString(logEntry)
	return err
}
