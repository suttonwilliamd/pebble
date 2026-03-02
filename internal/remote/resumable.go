package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TransferProgress tracks the progress of a resumable transfer
type TransferProgress struct {
	ObjectHash  string        `json:"object_hash"`
	TotalSize  int64         `json:"total_size"`
	Transferred int64        `json:"transferred"`
	Status     TransferStatus `json:"status"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// TransferStatus represents the status of a transfer
type TransferStatus string

const (
	TransferStatusPending    TransferStatus = "pending"
	TransferStatusInProgress TransferStatus = "in_progress"
	TransferStatusCompleted TransferStatus = "completed"
	TransferStatusFailed   TransferStatus = "failed"
	TransferStatusPaused   TransferStatus = "paused"
)

// ResumableClient supports resumable transfers
type ResumableClient struct {
	baseURL   string
	httpClient *http.Client
	auth      Auth
	chunkSize int64
}

// NewResumableClient creates a new resumable client
func NewResumableClient(baseURL string, auth Auth, chunkSize int64) *ResumableClient {
	if chunkSize == 0 {
		chunkSize = 1024 * 1024 // 1MB default
	}
	return &ResumableClient{
		baseURL:   baseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		auth:      auth,
		chunkSize: chunkSize,
	}
}

// UploadObject uploads an object with resumable support
func (c *ResumableClient) UploadObject(ctx context.Context, hash string, data []byte) error {
	objPath := fmt.Sprintf("%s/objects/%s", c.baseURL, hash)
	req, err := http.NewRequestWithContext(ctx, "PUT", objPath, bytes.NewReader(data))
	if err != nil {
		return err
	}

	c.addAuth(req)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %s - %s", resp.Status, string(body))
	}

	return nil
}

// DownloadObject downloads an object with resumable support
func (c *ResumableClient) DownloadObject(ctx context.Context, hash string) ([]byte, error) {
	objPath := fmt.Sprintf("%s/objects/%s", c.baseURL, hash)
	req, err := http.NewRequestWithContext(ctx, "GET", objPath, nil)
	if err != nil {
		return nil, err
	}

	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// PushWithProgress pushes with progress callback
func (c *ResumableClient) PushWithProgress(ctx context.Context, refs []RefInfo, objects map[string][]byte, progress func(string, int, int)) error {
	total := len(objects)
	completed := 0

	for hash, data := range objects {
		err := c.UploadObject(ctx, hash, data)
		if err != nil {
			return fmt.Errorf("failed to upload %s: %w", hash, err)
		}
		completed++
		if progress != nil {
			progress(hash, completed, total)
		}
	}

	return nil
}

// ListRemoteObjects lists objects on remote (for delta sync)
func (c *ResumableClient) ListRemoteObjects(ctx context.Context) (map[string]int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/objects", nil)
	if err != nil {
		return nil, err
	}

	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list failed: %s", resp.Status)
	}

	type ListResponse struct {
		Objects map[string]int64 `json:"objects"`
	}

	var result ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Objects, nil
}

// ComputeDelta determines which objects need to be uploaded
func ComputeDelta(localObjects, remoteObjects map[string]int64) (newObjects, updatedObjects, missingObjects map[string][]byte) {
	newObjects = make(map[string][]byte)
	updatedObjects = make(map[string][]byte)
	missingObjects = make(map[string][]byte)

	return newObjects, updatedObjects, missingObjects
}

func (c *ResumableClient) addAuth(req *http.Request) {
	if c.auth.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.auth.Token)
	} else if c.auth.Username != "" {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
}

// AccessControl defines basic access control rules
type AccessControl struct {
	Read  []string `json:"read"`  // Allowed users
	Write []string `json:"write"` // Allowed users for push
}

// ValidateAccess checks if user has required access
func ValidateAccess(ac *AccessControl, user string, write bool) bool {
	if ac == nil {
		return true // No ACL = open access
	}

	if write {
		for _, u := range ac.Write {
			if u == user || u == "*" {
				return true
			}
		}
		return false
	}

	// Read access
	for _, u := range ac.Read {
		if u == user || u == "*" {
			return true
		}
	}

	return false
}
