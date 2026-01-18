package errors

import (
	"fmt"
)

// FileNotFoundError represents a file not found error
type FileNotFoundError struct {
	FilePath string
}

// Error returns the error message
func (e *FileNotFoundError) Error() string {
	return fmt.Sprintf("file not found: %s", e.FilePath)
}

// DatabaseError represents a database error
type DatabaseError struct {
	Operation string
	Err       error
}

// Error returns the error message
func (e *DatabaseError) Error() string {
	return fmt.Sprintf("database error during %s: %v", e.Operation, e.Err)
}

// NetworkError represents a network error
type NetworkError struct {
	Operation string
	Err       error
}

// Error returns the error message
func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error during %s: %v", e.Operation, e.Err)
}

// InvalidInputError represents an invalid user input error
type InvalidInputError struct {
	Input string
}

// Error returns the error message
func (e *InvalidInputError) Error() string {
	return fmt.Sprintf("invalid input: %s", e.Input)
}

// AuthenticationError represents an authentication failure error
type AuthenticationError struct {
	Operation string
}

// Error returns the error message
func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("authentication failed during %s", e.Operation)
}
