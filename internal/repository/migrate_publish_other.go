//go:build !windows && !linux && !darwin

package repository

import "fmt"

func renameMigrationExclusive(source, destination string) error {
	return fmt.Errorf("exclusive migration publication is supported on Windows, Linux and macOS only")
}
