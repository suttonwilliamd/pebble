package remote

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:8080", Auth{
		Token: "test-token",
	})
	
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	
	if client.baseURL != "http://localhost:8080" {
		t.Errorf("baseURL = %v, want http://localhost:8080", client.baseURL)
	}
}

func TestAuth_SetToken(t *testing.T) {
	auth := Auth{
		Token: "mytoken",
	}
	
	if auth.Token != "mytoken" {
		t.Errorf("Token = %v, want mytoken", auth.Token)
	}
}

func TestAuth_SetBasicAuth(t *testing.T) {
	auth := Auth{
		Username: "user",
		Password: "pass",
	}
	
	if auth.Username != "user" {
		t.Errorf("Username = %v, want user", auth.Username)
	}
	if auth.Password != "pass" {
		t.Errorf("Password = %v, want pass", auth.Password)
	}
}

func TestRemoteInfo_JSON(t *testing.T) {
	info := RemoteInfo{
		Name:      "origin",
		URL:       "https://github.com/user/repo",
		LastPush:  "2024-01-01T00:00:00Z",
		LastPull:  "2024-01-02T00:00:00Z",
	}
	
	if info.Name != "origin" {
		t.Errorf("Name = %v, want origin", info.Name)
	}
	if info.URL != "https://github.com/user/repo" {
		t.Errorf("URL = %v, want https://github.com/user/repo", info.URL)
	}
}

func TestObjectInfo(t *testing.T) {
	obj := ObjectInfo{
		Hash: "abc123",
		Type: "blob",
		Size: 1024,
	}
	
	if obj.Hash != "abc123" {
		t.Errorf("Hash = %v, want abc123", obj.Hash)
	}
	if obj.Type != "blob" {
		t.Errorf("Type = %v, want blob", obj.Type)
	}
	if obj.Size != 1024 {
		t.Errorf("Size = %v, want 1024", obj.Size)
	}
}

func TestRefInfo(t *testing.T) {
	ref := RefInfo{
		Name: "refs/heads/main",
		Hash: "abc123def456",
	}
	
	if ref.Name != "refs/heads/main" {
		t.Errorf("Name = %v, want refs/heads/main", ref.Name)
	}
	if ref.Hash != "abc123def456" {
		t.Errorf("Hash = %v, want abc123def456", ref.Hash)
	}
}

func TestListRefsResponse(t *testing.T) {
	resp := ListRefsResponse{
		Refs: []RefInfo{
			{Name: "refs/heads/main", Hash: "abc123"},
			{Name: "refs/heads/dev", Hash: "def456"},
		},
	}
	
	if len(resp.Refs) != 2 {
		t.Errorf("Refs count = %d, want 2", len(resp.Refs))
	}
}

func TestListObjectsResponse(t *testing.T) {
	resp := ListObjectsResponse{
		Objects: []ObjectInfo{
			{Hash: "abc", Type: "blob", Size: 100},
			{Hash: "def", Type: "tree", Size: 200},
		},
		HasMore: true,
	}
	
	if len(resp.Objects) != 2 {
		t.Errorf("Objects count = %d, want 2", len(resp.Objects))
	}
	if !resp.HasMore {
		t.Error("Expected HasMore to be true")
	}
}
