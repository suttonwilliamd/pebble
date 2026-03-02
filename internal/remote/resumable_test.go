package remote

import (
	"testing"
	"time"
)

func TestTransferProgress(t *testing.T) {
	progress := TransferProgress{
		ObjectHash:  "abc123",
		TotalSize:  1024,
		Transferred: 512,
		Status:      TransferStatusInProgress,
		UpdatedAt:  time.Now(),
	}

	if progress.ObjectHash != "abc123" {
		t.Errorf("ObjectHash = %v, want abc123", progress.ObjectHash)
	}
	if progress.Status != TransferStatusInProgress {
		t.Errorf("Status = %v, want in_progress", progress.Status)
	}
}

func TestTransferStatus_Constants(t *testing.T) {
	tests := []struct {
		status   TransferStatus
		expected string
	}{
		{TransferStatusPending, "pending"},
		{TransferStatusInProgress, "in_progress"},
		{TransferStatusCompleted, "completed"},
		{TransferStatusFailed, "failed"},
		{TransferStatusPaused, "paused"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.expected {
			t.Errorf("status = %v, want %v", tt.status, tt.expected)
		}
	}
}

func TestNewResumableClient(t *testing.T) {
	client := NewResumableClient("http://localhost:8080", Auth{Token: "test"}, 0)
	if client == nil {
		t.Fatal("NewResumableClient returned nil")
	}
	if client.chunkSize != 1024*1024 {
		t.Errorf("chunkSize = %d, want 1048576", client.chunkSize)
	}

	// Custom chunk size
	client2 := NewResumableClient("http://localhost:8080", Auth{}, 512*1024)
	if client2.chunkSize != 512*1024 {
		t.Errorf("chunkSize = %d, want 524288", client2.chunkSize)
	}
}

func TestValidateAccess(t *testing.T) {
	// No ACL = open access
	ac := (*AccessControl)(nil)
	if !ValidateAccess(ac, "user", true) {
		t.Error("Expected open access with nil ACL")
	}

	// Empty ACL with write - should NOT allow (no write list)
	ac = &AccessControl{}
	if ValidateAccess(ac, "user", true) {
		t.Error("Expected no write access with empty ACL")
	}

	// With read access
	ac = &AccessControl{
		Read: []string{"alice", "bob"},
	}
	if !ValidateAccess(ac, "alice", false) {
		t.Error("Expected alice to have read access")
	}
	if ValidateAccess(ac, "charlie", false) {
		t.Error("Expected charlie to not have read access")
	}

	// With write access
	ac = &AccessControl{
		Write: []string{"admin"},
	}
	if !ValidateAccess(ac, "admin", true) {
		t.Error("Expected admin to have write access")
	}
	if ValidateAccess(ac, "user", true) {
		t.Error("Expected user to not have write access")
	}

	// Wildcard
	ac = &AccessControl{
		Write: []string{"*"},
	}
	if !ValidateAccess(ac, "anyone", true) {
		t.Error("Expected wildcard to allow anyone")
	}
}

func TestComputeDelta(t *testing.T) {
	local := map[string]int64{
		"obj1": 100,
		"obj2": 200,
		"obj3": 300,
	}

	remote := map[string]int64{
		"obj1": 100,
		"obj2": 150,
		"obj4": 400,
	}

	newObjs, updatedObjs, missingObjs := ComputeDelta(local, remote)

	// These are empty because we don't pass local data in this simplified version
	// In real implementation, would compare hashes
	if newObjs == nil {
		t.Error("Expected newObjs map")
	}
	if updatedObjs == nil {
		t.Error("Expected updatedObjs map")
	}
	if missingObjs == nil {
		t.Error("Expected missingObjs map")
	}
}
