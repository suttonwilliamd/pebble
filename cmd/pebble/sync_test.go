package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/syncproto"
)

func TestRemoteCLI(t *testing.T) {
	t.Setenv("PEBBLE_AUTHOR", "Test")
	t.Setenv("PEBBLE_EMAIL", "test@example.com")
	token := strings.Repeat("a1", 32)
	secret := filepath.Join(t.TempDir(), "token")
	os.WriteFile(secret, []byte(token), 0600)
	t.Setenv("PEBBLE_TOKEN_FILE", secret)
	r, err := snapshot.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := syncproto.NewHandler(r, token)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	a, b := t.TempDir(), t.TempDir()
	var out bytes.Buffer
	call := func(root string, args ...string) {
		t.Helper()
		if err := run(args, root, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	call(a, "init")
	os.WriteFile(filepath.Join(a, "file"), []byte("hello remote"), 0644)
	call(a, "commit", "first")
	call(a, "push", server.URL)
	call(b, "init")
	call(b, "fetch", server.URL)
	if _, err := os.Stat(filepath.Join(b, "file")); !os.IsNotExist(err) {
		t.Fatal("fetch changed files")
	}
	call(b, "checkout", "incoming/main")
	got, _ := os.ReadFile(filepath.Join(b, "file"))
	if string(got) != "hello remote" {
		t.Fatal("remote snapshot not restored")
	}
	if err := run([]string{"fetch", server.URL}, b, &out); err == nil {
		t.Fatal("fetch updated checked-out incoming branch")
	}
	call(b, "branch", "work")
	call(b, "checkout", "work")
	os.WriteFile(filepath.Join(b, "file"), []byte("B edit"), 0644)
	call(b, "commit", "B")
	call(b, "push", server.URL, "main")
	call(b, "fsck")
	if err := run([]string{"push", server.URL}, a, &out); err == nil {
		t.Fatal("CLI stale push allowed")
	}
}
