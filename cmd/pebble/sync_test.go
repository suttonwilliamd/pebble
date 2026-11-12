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

func TestLegacyRemoteCLI(t *testing.T) {
	url := os.Getenv("PEBBLE_TEST_REMOTE_URL") // Explicit disposable CI service only.
	if url == "" {
		token := strings.Repeat("a1", 32)
		secret := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(secret, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PEBBLE_TOKEN_FILE", secret)
		remote, err := snapshot.Init(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		handler, err := syncproto.NewHandler(remote, token)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		url = server.URL
	}
	a, err := snapshot.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := snapshot.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	call := func(repo *snapshot.Repository, args ...string) {
		t.Helper()
		if err := run(args, repo.RootPath(), &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(a.RootPath(), "file"), []byte("hello remote"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit("first", "Test", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	call(a, "push", url)
	call(b, "fetch", url)
	if _, err := os.Stat(filepath.Join(b.RootPath(), "file")); !os.IsNotExist(err) {
		t.Fatal("fetch changed files")
	}
	if err := b.Checkout("incoming/main"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(b.RootPath(), "file"))
	if err != nil || string(got) != "hello remote" {
		t.Fatalf("remote snapshot: %q %v", got, err)
	}
	if err := run([]string{"fetch", url}, b.RootPath(), &out); err == nil {
		t.Fatal("fetch updated checked-out incoming branch")
	}
	if err := b.CreateBranch("work"); err != nil {
		t.Fatal(err)
	}
	if err := b.Checkout("work"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.RootPath(), "file"), []byte("B edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Commit("B", "Test", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	call(b, "push", url, "main")
	if _, err := b.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"push", url}, a.RootPath(), &out); err == nil {
		t.Fatal("CLI stale push allowed")
	}
}

// CI invokes this only after restarting its disposable container.
func TestLegacyRemotePersistence(t *testing.T) {
	url := os.Getenv("PEBBLE_TEST_REMOTE_URL")
	if url == "" {
		t.Skip("requires an explicitly selected disposable CI service")
	}
	repo, err := snapshot.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = run([]string{"fetch", url}, repo.RootPath(), &out); err != nil {
		t.Fatal(err)
	}
	if err = repo.Checkout("incoming/main"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(repo.RootPath(), "file"))
	if err != nil || string(got) != "B edit" {
		t.Fatalf("persisted content %q: %v", got, err)
	}
	if _, err = repo.Verify(); err != nil {
		t.Fatal(err)
	}
}
