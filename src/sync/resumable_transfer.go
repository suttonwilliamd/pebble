package sync

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

// ResumableTransfer is responsible for handling resumable transfers
type ResumableTransfer struct {
	Client *http.Client
}

// NewResumableTransfer creates a new ResumableTransfer instance
func NewResumableTransfer(client *http.Client) *ResumableTransfer {
	return &ResumableTransfer{Client: client}
}

// DownloadWithResume downloads a file with support for resuming
func (rt *ResumableTransfer) DownloadWithResume(url, filePath string) error {
	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Download the file
	resp, err := rt.Client.Get(url)
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

// UploadWithResume uploads a file with support for resuming
func (rt *ResumableTransfer) UploadWithResume(url, filePath string) error {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get the file info
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	// Create the request
	req, err := http.NewRequest("PUT", url, file)
	if err != nil {
		return err
	}

	// Set the Content-Length header
	req.ContentLength = fileInfo.Size()

	// Send the request
	resp, err := rt.Client.Do(req)
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

// ResumeDownload resumes a download from where it left off
func (rt *ResumableTransfer) ResumeDownload(url, filePath string, offset int64) error {
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
	resp, err := rt.Client.Do(req)
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
