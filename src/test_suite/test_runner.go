package test_suite

import (
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

// TestSuite represents a collection of tests with metadata
type TestSuite struct {
	Name  string
	Tests []TestCase
}

// TestCase represents an individual test with metadata
type TestCase struct {
	Name string
	Fn   func(*testing.T)
}

// Runner handles the execution and reporting of test suites
type Runner struct {
	Suites []TestSuite
	Colors struct {
		Pass    *color.Color
		Fail    *color.Color
		Pending *color.Color
		Info    *color.Color
		Debug   *color.Color
	}
	Verbose bool
}

// NewRunner creates a new test runner with default color settings
func NewRunner() *Runner {
	r := &Runner{}
	r.Colors.Pass = color.New(color.FgGreen, color.Bold)
	r.Colors.Fail = color.New(color.FgRed, color.Bold)
	r.Colors.Pending = color.New(color.FgYellow, color.Bold)
	r.Colors.Info = color.New(color.FgBlue, color.Bold)
	r.Colors.Debug = color.New(color.FgCyan)
	return r
}

// EnableVerbose enables detailed debug output
func (r *Runner) EnableVerbose() {
	r.Verbose = true
}

// AddSuite adds a test suite to the runner
func (r *Runner) AddSuite(suite TestSuite) {
	r.Suites = append(r.Suites, suite)
}

// Run executes all test suites and returns the results
func (r *Runner) Run() bool {
	totalTests := 0
	passedTests := 0
	failedTests := 0

	for _, suite := range r.Suites {
		totalTests += len(suite.Tests)
	}

	r.Colors.Info.Printf("\n🚀 Starting Test Suite: %d total tests\n\n", totalTests)

	if r.Verbose {
		r.Colors.Debug.Printf("🔍 Verbose mode enabled - showing detailed information\n\n")
	}

	allPassed := true
	for _, suite := range r.Suites {
		suitePassed := true
		r.Colors.Info.Printf("📁 Running suite: %s\n", suite.Name)

		if r.Verbose {
			r.Colors.Debug.Printf("  📝 Suite contains %d tests\n", len(suite.Tests))
		}

		for _, test := range suite.Tests {
			if r.Verbose {
				r.Colors.Debug.Printf("  🔍 Starting test: %s\n", test.Name)
			}

			t := &testing.T{}
			// Capture test output
			test.Fn(t)

			if t.Failed() {
				r.Colors.Fail.Printf("  ❌ %s\n", test.Name)
				if r.Verbose {
					r.Colors.Debug.Printf("  💡 Test failed - check implementation\n")
				}
				failedTests++
				suitePassed = false
				allPassed = false
			} else {
				r.Colors.Pass.Printf("  ✅ %s\n", test.Name)
				if r.Verbose {
					r.Colors.Debug.Printf("  🎯 Test passed successfully\n")
				}
				passedTests++
			}
		}

		if suitePassed {
			r.Colors.Pass.Printf("✅ Suite %s passed\n\n", suite.Name)
		} else {
			r.Colors.Fail.Printf("❌ Suite %s failed\n\n", suite.Name)
		}
	}

	// Print summary
	r.Colors.Info.Printf("\n📊 Test Summary:\n")
	r.Colors.Info.Printf("  Total: %d\n", totalTests)
	r.Colors.Pass.Printf("  Passed: %d\n", passedTests)
	r.Colors.Fail.Printf("  Failed: %d\n", failedTests)

	if passedTests > 0 {
		r.Colors.Pass.Printf("  Success Rate: %.2f%%\n", float64(passedTests)*100/float64(totalTests))
	}

	if r.Verbose {
		r.Colors.Debug.Printf("\n📈 Detailed Statistics:\n")
		r.Colors.Debug.Printf("  Test suites run: %d\n", len(r.Suites))
		r.Colors.Debug.Printf("  Average success rate: %.2f%%\n", float64(passedTests)*100/float64(totalTests))
		if failedTests > 0 {
			r.Colors.Debug.Printf("  Failure rate: %.2f%%\n", float64(failedTests)*100/float64(totalTests))
		}
	}

	if allPassed {
		r.Colors.Pass.Printf("\n🎉 All tests passed! 🎉\n")
		r.Colors.Info.Printf("🚀 Ready for deployment!\n")
	} else {
		r.Colors.Fail.Printf("\n💥 Some tests failed! 💥\n")
		r.Colors.Pending.Printf("⚠️  Please review the failures above\n")
		if r.Verbose {
			r.Colors.Debug.Printf("🔧 Debugging tip: Use 'go test -v' for more detailed Go test output\n")
		}
	}

	return allPassed
}

// Helper functions for test assertions
func AssertEqual(t *testing.T, expected, actual interface{}, message string) {
	assert.Equal(t, expected, actual, message)
}

func AssertTrue(t *testing.T, condition bool, message string) {
	assert.True(t, condition, message)
}

func AssertFalse(t *testing.T, condition bool, message string) {
	assert.False(t, condition, message)
}

func AssertNil(t *testing.T, object interface{}, message string) {
	assert.Nil(t, object, message)
}

func AssertNotNil(t *testing.T, object interface{}, message string) {
	assert.NotNil(t, object, message)
}
