package errors

import (
	"fmt"
	"log"
	"os"
)

// ErrorHandler is responsible for handling errors gracefully
type ErrorHandler struct {
	LogFile *os.File
}

// NewErrorHandler creates a new ErrorHandler instance
func NewErrorHandler(logFilePath string) (*ErrorHandler, error) {
	// Open the log file
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	return &ErrorHandler{LogFile: logFile}, nil
}

// HandleError handles an error gracefully
func (eh *ErrorHandler) HandleError(err error, message string) {
	// Log the error
	eh.logError(err, message)

	// Print the error to the console
	fmt.Printf("Error: %s\n", message)
}

// logError logs an error to the log file
func (eh *ErrorHandler) logError(err error, message string) {
	// Write the error to the log file
	log.SetOutput(eh.LogFile)
	log.Printf("%s: %v\n", message, err)
}

// Close closes the log file
func (eh *ErrorHandler) Close() error {
	return eh.LogFile.Close()
}
