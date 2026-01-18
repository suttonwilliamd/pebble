package errors

import (
	"fmt"
	"os"
	"testing"
)

func TestErrorHandler(t *testing.T) {
	// Create a temporary log file
	logFile, err := os.CreateTemp("", "test_error_handler")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(logFile.Name())

	// Create ErrorHandler instance
	eh, err := NewErrorHandler(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer eh.Close()

	// Test error handling
	testError := fmt.Errorf("test error")
	eh.HandleError(testError, "Test error message")

	// Verify log file is created
	if _, err := os.Stat(logFile.Name()); os.IsNotExist(err) {
		t.Error("Log file not created")
	}
}
