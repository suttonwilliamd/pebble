package errors

import (
	"fmt"
	"time"
)

// ErrorCode represents a category of errors
type ErrorCode string

const (
	// Repository errors
	ErrCodeNotInitialized ErrorCode = "NOT_INITIALIZED"
	ErrCodeAlreadyInitialized ErrorCode = "ALREADY_INITIALIZED"
	ErrCodeInvalidPath ErrorCode = "INVALID_PATH"
	
	// Object errors  
	ErrCodeObjectNotFound ErrorCode = "OBJECT_NOT_FOUND"
	ErrCodeObjectCorrupted ErrorCode = "OBJECT_CORRUPTED"
	
	// Commit errors
	ErrCodeNoCommits ErrorCode = "NO_COMMITS"
	ErrCodeInvalidCommit ErrorCode = "INVALID_COMMIT"
	
	// Ref errors
	ErrCodeRefNotFound ErrorCode = "REF_NOT_FOUND"
	ErrCodeRefConflict ErrorCode = "REF_CONFLICT"
	
	// Network errors
	ErrCodeNetwork ErrorCode = "NETWORK_ERROR"
	ErrCodeTimeout ErrorCode = "TIMEOUT"
	ErrCodeAuthFailed ErrorCode = "AUTH_FAILED"
	
	// Storage errors
	ErrCodeStorageFull ErrorCode = "STORAGE_FULL"
	ErrCodeStorageError ErrorCode = "STORAGE_ERROR"
	
	// User input errors
	ErrCodeInvalidInput ErrorCode = "INVALID_INPUT"
	ErrCodeMissingArg ErrorCode = "MISSING_ARGUMENT"
)

// PebbleError represents an error with context
type PebbleError struct {
	Code    ErrorCode   `json:"code"`
	Message string      `json:"message"`
	Cause   error      `json:"cause,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func (e *PebbleError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *PebbleError) Unwrap() error {
	return e.Cause
}

// New creates a new PebbleError
func New(code ErrorCode, message string) *PebbleError {
	return &PebbleError{
		Code:     code,
		Message:  message,
		Timestamp: time.Now(),
	}
}

// Wrap wraps an existing error
func Wrap(code ErrorCode, message string, cause error) *PebbleError {
	return &PebbleError{
		Code:     code,
		Message:  message,
		Cause:    cause,
		Timestamp: time.Now(),
	}
}

// Is checks if the error matches the code
func (e *PebbleError) Is(code ErrorCode) bool {
	return e.Code == code
}

// Helper constructors

// NotInitialized returns an error for uninitialized repos
func NotInitialized(path string) *PebbleError {
	return New(ErrCodeNotInitialized, fmt.Sprintf("repository not initialized at %s", path))
}

// AlreadyInitialized returns an error for already initialized repos
func AlreadyInitialized(path string) *PebbleError {
	return New(ErrCodeAlreadyInitialized, fmt.Sprintf("repository already initialized at %s", path))
}

// ObjectNotFound returns an error for missing objects
func ObjectNotFound(hash string) *PebbleError {
	return New(ErrCodeObjectNotFound, fmt.Sprintf("object not found: %s", hash))
}

// RefNotFound returns an error for missing refs
func RefNotFound(ref string) *PebbleError {
	return New(ErrCodeRefNotFound, fmt.Sprintf("reference not found: %s", ref))
}

// NetworkError returns a network error
func NetworkError(message string, cause error) *PebbleError {
	return Wrap(ErrCodeNetwork, message, cause)
}

// AuthFailed returns an authentication error
func AuthFailed(message string) *PebbleError {
	return New(ErrCodeAuthFailed, message)
}

// InvalidInput returns an input validation error
func InvalidInput(message string) *PebbleError {
	return New(ErrCodeInvalidInput, message)
}

// MissingArg returns a missing argument error
func MissingArg(arg string) *PebbleError {
	return New(ErrCodeMissingArg, fmt.Sprintf("missing required argument: %s", arg))
}

// NoCommits returns an error for repos with no commits
func NoCommits() *PebbleError {
	return New(ErrCodeNoCommits, "no commits in repository")
}
