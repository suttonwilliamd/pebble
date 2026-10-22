package syncproto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTokenFile(t *testing.T) {
	if _, err := ReadTokenFile(""); err == nil {
		t.Fatal("empty path accepted")
	}
	for _, token := range []string{"short", strings.Repeat("x", 4097), strings.Repeat("x", 32) + "\ninjected", strings.Repeat("x", 32) + " space"} {
		p := filepath.Join(t.TempDir(), "token")
		os.WriteFile(p, []byte(token), 0600)
		if _, err := ReadTokenFile(p); err == nil {
			t.Fatal("bad token accepted")
		}
	}
	p := filepath.Join(t.TempDir(), "token")
	os.WriteFile(p, []byte(testToken+"\r\n"), 0600)
	if got, err := ReadTokenFile(p); err != nil || got != testToken {
		t.Fatalf("valid token: %v", err)
	}
}
