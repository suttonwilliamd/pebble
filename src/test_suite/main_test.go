package test_suite

import (
	"testing"
)

// TestMain is the entry point for running all test suites
func TestMain(t *testing.T) {
	runner := NewRunner()

	// Enable verbose mode for detailed output
	runner.EnableVerbose()

	// Add all test suites
	runner.AddSuite(ExampleTestSuite())
	runner.AddSuite(MathTestSuite())
	runner.AddSuite(LogicTestSuite())

	// Run all tests
	success := runner.Run()

	// Exit with appropriate status
	if !success {
		t.FailNow()
	}
}
