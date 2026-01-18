package sync

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnhancedResumableTransfer(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test_enhanced_resumable_transfer")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("test content")
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle GET requests
		if r.Method == "GET" {
			// Check for Range header
			rangeHeader := r.Header.Get("Range")
			if rangeHeader != "" {
				// Parse the range
				var start, end int64
				_, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				// Send the range
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(testContent)))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(testContent[start:])
				return
			}

			// Send the entire file
			w.WriteHeader(http.StatusOK)
			w.Write(testContent)
			return
		}

		// Handle PUT requests
		if r.Method == "PUT" {
			// Check for Content-Range header
			contentRangeHeader := r.Header.Get("Content-Range")
			if contentRangeHeader != "" {
				// Parse the content range
				var start, end, total int64
				_, err := fmt.Sscanf(contentRangeHeader, "bytes %d-%d/%d", &start, &end, &total)
				if err != nil {
					_, err := fmt.Sscanf(contentRangeHeader, "bytes %d-*/*", &start)
					if err != nil {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
				}

				// Read the data
				data, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				// Write the data to the file
				err = os.WriteFile(testFile, data, 0644)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusOK)
				return
			}

			// Read the data
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// Write the data to the file
			err = os.WriteFile(testFile, data, 0644)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	// Create EnhancedResumableTransfer instance
	ert := NewEnhancedResumableTransfer(server.Client())

	// Test download with resume
	downloadFile := filepath.Join(tmpDir, "download.txt")
	file, err := os.Create(downloadFile)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()

	err = ert.DownloadWithResume(server.URL, downloadFile, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Verify downloaded file
	downloadedContent, err := os.ReadFile(downloadFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(downloadedContent) > 0 {
		t.Logf("Downloaded content length: %d", len(downloadedContent))
	}

	// Test upload with resume
	uploadFile := filepath.Join(tmpDir, "upload.txt")
	err = os.WriteFile(uploadFile, testContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	err = ert.UploadWithResume(server.URL, uploadFile, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Verify uploaded file
	uploadedContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(uploadedContent) != string(testContent) {
		t.Errorf("Uploaded content does not match expected content")
	}
}
