package test_suite

import (
	"testing"
)

// ExampleTestSuite demonstrates how to create and use a test suite
func ExampleTestSuite() TestSuite {
	return TestSuite{
		Name: "Example Tests",
		Tests: []TestCase{
			{
				Name: "Test Addition",
				Fn: func(t *testing.T) {
					result := 1 + 1
					AssertEqual(t, 2, result, "1 + 1 should equal 2")
				},
			},
			{
				Name: "Test String Concatenation",
				Fn: func(t *testing.T) {
					result := "Hello, " + "World!"
					AssertEqual(t, "Hello, World!", result, "String concatenation should work")
				},
			},
			{
				Name: "Test Array Length",
				Fn: func(t *testing.T) {
					arr := []int{1, 2, 3, 4, 5}
					AssertEqual(t, 5, len(arr), "Array should have 5 elements")
				},
			},
			{
				Name: "Test Failing Test",
				Fn: func(t *testing.T) {
					result := 1 + 1
					AssertEqual(t, 3, result, "This test should fail: 1 + 1 does not equal 3")
				},
			},
		},
	}
}

// MathTestSuite demonstrates a more focused test suite
func MathTestSuite() TestSuite {
	return TestSuite{
		Name: "Math Operations",
		Tests: []TestCase{
			{
				Name: "Test Multiplication",
				Fn: func(t *testing.T) {
					result := 3 * 4
					AssertEqual(t, 12, result, "3 * 4 should equal 12")
				},
			},
			{
				Name: "Test Division",
				Fn: func(t *testing.T) {
					result := 10 / 2
					AssertEqual(t, 5, result, "10 / 2 should equal 5")
				},
			},
			{
				Name: "Test Subtraction",
				Fn: func(t *testing.T) {
					result := 10 - 5
					AssertEqual(t, 5, result, "10 - 5 should equal 5")
				},
			},
		},
	}
}

// LogicTestSuite demonstrates testing boolean logic
func LogicTestSuite() TestSuite {
	return TestSuite{
		Name: "Boolean Logic",
		Tests: []TestCase{
			{
				Name: "Test AND Operation",
				Fn: func(t *testing.T) {
					result := true && false
					AssertFalse(t, result, "true AND false should be false")
				},
			},
			{
				Name: "Test OR Operation",
				Fn: func(t *testing.T) {
					result := true || false
					AssertTrue(t, result, "true OR false should be true")
				},
			},
			{
				Name: "Test NOT Operation",
				Fn: func(t *testing.T) {
					result := !true
					AssertFalse(t, result, "NOT true should be false")
				},
			},
		},
	}
}
