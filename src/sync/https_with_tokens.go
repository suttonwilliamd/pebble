package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HTTPSWithTokens is responsible for handling HTTPS authentication with tokens
type HTTPSWithTokens struct {
	Client *http.Client
	Token  string
}

// NewHTTPSWithTokens creates a new HTTPSWithTokens instance
func NewHTTPSWithTokens(client *http.Client, token string) *HTTPSWithTokens {
	return &HTTPSWithTokens{Client: client, Token: token}
}

// Authenticate authenticates with the server using the token
func (hwt *HTTPSWithTokens) Authenticate(url string) error {
	// Create the request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	// Set the Authorization header
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", hwt.Token))

	// Send the request
	resp, err := hwt.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed with status code %d", resp.StatusCode)
	}

	return nil
}

// GetToken retrieves a token from the server
func (hwt *HTTPSWithTokens) GetToken(url, username, password string) (string, error) {
	// Create the request body
	requestBody := map[string]string{
		"username": username,
		"password": password,
	}

	// Marshal the request body
	requestBodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	// Create the request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBodyJSON))
	if err != nil {
		return "", err
	}

	// Set the Content-Type header
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := hwt.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Unmarshal the response
	var response map[string]string
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return "", err
	}

	// Get the token
	token, ok := response["token"]
	if !ok {
		return "", fmt.Errorf("token not found in response")
	}

	return token, nil
}
