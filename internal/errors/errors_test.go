package errors

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(ErrCodeNotInitialized, "test error")
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if err.Code != ErrCodeNotInitialized {
		t.Errorf("Code = %v, want %v", err.Code, ErrCodeNotInitialized)
	}
	if err.Message != "test error" {
		t.Errorf("Message = %v, want 'test error'", err.Message)
	}
}

func TestWrap(t *testing.T) {
	original := errors.New("original error")
	err := Wrap(ErrCodeNetwork, "wrapped error", original)
	
	if err.Code != ErrCodeNetwork {
		t.Errorf("Code = %v, want %v", err.Code, ErrCodeNetwork)
	}
	if err.Cause != original {
		t.Error("Cause should be original error")
	}
}

func TestError(t *testing.T) {
	err := New(ErrCodeObjectNotFound, "object not found")
	msg := err.Error()
	
	if msg == "" {
		t.Error("Error message should not be empty")
	}
}

func TestIs(t *testing.T) {
	err := New(ErrCodeNotInitialized, "test")
	
	if !err.Is(ErrCodeNotInitialized) {
		t.Error("Expected Is to return true for matching code")
	}
	if err.Is(ErrCodeObjectNotFound) {
		t.Error("Expected Is to return false for non-matching code")
	}
}

func TestNotInitialized(t *testing.T) {
	err := NotInitialized("/path/to/repo")
	if err.Code != ErrCodeNotInitialized {
		t.Errorf("Code = %v, want NOT_INITIALIZED", err.Code)
	}
}

func TestAlreadyInitialized(t *testing.T) {
	err := AlreadyInitialized("/path/to/repo")
	if err.Code != ErrCodeAlreadyInitialized {
		t.Errorf("Code = %v, want ALREADY_INITIALIZED", err.Code)
	}
}

func TestObjectNotFound(t *testing.T) {
	err := ObjectNotFound("abc123")
	if err.Code != ErrCodeObjectNotFound {
		t.Errorf("Code = %v, want OBJECT_NOT_FOUND", err.Code)
	}
}

func TestRefNotFound(t *testing.T) {
	err := RefNotFound("refs/heads/main")
	if err.Code != ErrCodeRefNotFound {
		t.Errorf("Code = %v, want REF_NOT_FOUND", err.Code)
	}
}

func TestNetworkError(t *testing.T) {
	original := errors.New("connection refused")
	err := NetworkError("failed to connect", original)
	
	if err.Code != ErrCodeNetwork {
		t.Errorf("Code = %v, want NETWORK_ERROR", err.Code)
	}
	if err.Cause != original {
		t.Error("Cause should be original error")
	}
}

func TestAuthFailed(t *testing.T) {
	err := AuthFailed("invalid token")
	if err.Code != ErrCodeAuthFailed {
		t.Errorf("Code = %v, want AUTH_FAILED", err.Code)
	}
}

func TestInvalidInput(t *testing.T) {
	err := InvalidInput("invalid branch name")
	if err.Code != ErrCodeInvalidInput {
		t.Errorf("Code = %v, want INVALID_INPUT", err.Code)
	}
}

func TestMissingArg(t *testing.T) {
	err := MissingArg("message")
	if err.Code != ErrCodeMissingArg {
		t.Errorf("Code = %v, want MISSING_ARGUMENT", err.Code)
	}
}

func TestNoCommits(t *testing.T) {
	err := NoCommits()
	if err.Code != ErrCodeNoCommits {
		t.Errorf("Code = %v, want NO_COMMITS", err.Code)
	}
}

func TestUnwrap(t *testing.T) {
	original := errors.New("original")
	err := Wrap(ErrCodeNetwork, "wrapped", original)
	
	unwrapped := err.Unwrap()
	if unwrapped != original {
		t.Error("Unwrap should return the cause")
	}
}
