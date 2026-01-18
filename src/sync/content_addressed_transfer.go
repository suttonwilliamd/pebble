package sync

import (
	"fmt"
	"io"
	"net/http"
)

// ContentAddressedTransfer is responsible for transferring objects using content-addressed identifiers
type ContentAddressedTransfer struct {
	Client *http.Client
}

// NewContentAddressedTransfer creates a new ContentAddressedTransfer instance
func NewContentAddressedTransfer(client *http.Client) *ContentAddressedTransfer {
	return &ContentAddressedTransfer{Client: client}
}

// DownloadObject downloads an object using its content-addressed identifier
func (cat *ContentAddressedTransfer) DownloadObject(url, hash string) ([]byte, error) {
	// Create the request
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s", url, hash), nil)
	if err != nil {
		return nil, err
	}

	// Send the request
	resp, err := cat.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read the response
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// UploadObject uploads an object using its content-addressed identifier
func (cat *ContentAddressedTransfer) UploadObject(url, hash string, data []byte) error {
	// Create the request
	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/%s", url, hash), nil)
	if err != nil {
		return err
	}

	// Send the request
	resp, err := cat.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload failed with status code %d", resp.StatusCode)
	}

	return nil
}

// ResumeTransfer resumes a transfer from where it left off
func (cat *ContentAddressedTransfer) ResumeTransfer(url, hash string, offset int64) ([]byte, error) {
	// Create the request with Range header
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s", url, hash), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))

	// Send the request
	resp, err := cat.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read the response
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}
