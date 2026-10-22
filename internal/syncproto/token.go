package syncproto

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ReadTokenFile reads a bounded secret file; tokens never need to appear in process arguments.
func ReadTokenFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("set PEBBLE_TOKEN_FILE to a private token file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot open token file: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	if len(b) > 4096 {
		return "", fmt.Errorf("token file is too large")
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 {
		return "", fmt.Errorf("token must contain at least 32 characters")
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return "", fmt.Errorf("token must be printable ASCII without spaces")
		}
	}
	return token, nil
}
