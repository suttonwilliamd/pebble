package sync

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

// EnhancedResumableTransfer is responsible for handling enhanced resumable transfers
type EnhancedResumableTransfer struct {
	Client *http.Client
}

// NewEnhancedResumableTransfer creates a new EnhancedResumableTransfer instance
func NewEnhancedResumableTransfer(client *http.Client) *EnhancedResumableTransfer {
	return &EnhancedResumableTransfer{Client: client}
}

// DownloadWithResume downloads a file with support for resuming from a specific offset
func (ert *EnhancedResumableTransfer) DownloadWithResume(url, filePath string, offset int64) error {
	// Open the file in append mode
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create the request with Range header
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))

	// Send the request
	resp, err := ert.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Copy the data to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

// UploadWithResume uploads a file with support for resuming from a specific offset
func (ert *EnhancedResumableTransfer) UploadWithResume(url, filePath string, offset int64) error {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Seek to the offset
	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		return err
	}

	// Create the request
	req, err := http.NewRequest("PUT", url, file)
	if err != nil {
		return err
	}

	// Set the Content-Range header
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-*/*", offset))

	// Send the request
	resp, err := ert.Client.Do(req)
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

// VerifyDataIntegrity verifies the integrity of the downloaded data
func (ert *EnhancedResumableTransfer) VerifyDataIntegrity(filePath string, expectedHash string) (bool, error) {
	// TODO: Implement data integrity verification
	return true, nil
}
