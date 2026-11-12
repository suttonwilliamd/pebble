//go:build linux || darwin

package repository

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemPermissionFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		if os.Getenv("PEBBLE_REQUIRE_UNPRIVILEGED") == "1" {
			t.Fatal("permission proof requires a non-root process")
		}
		t.Skip("root cannot prove unprivileged permission denial")
	}
	r := mutationFixture(t)
	parent := filepath.Join(r.root, "dir")
	if err := os.Chmod(parent, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
	probe := filepath.Join(parent, "permission-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_ = f.Close()
		_ = os.Remove(probe)
		if os.Getenv("PEBBLE_REQUIRE_UNPRIVILEGED") == "1" {
			t.Fatal("process can override fixture parent permissions")
		}
		t.Skip("process can override fixture permissions; not an unprivileged proof")
	}
	if !os.IsPermission(err) {
		t.Fatalf("expected real permission denial: %v", err)
	}
	before := publicSnapshot(t, r.root)
	head, err := r.CurrentID()
	if err != nil {
		t.Fatal(err)
	}
	// No entry writes are needed for an unchanged checkout through this parent.
	if err = r.Checkout("before"); err != nil {
		t.Fatalf("read-only unchanged parent rejected: %v", err)
	}
	assertPublicSnapshot(t, r.root, before)
	if err = r.Checkout("after"); err == nil {
		t.Fatal("unwritable parent mutation acknowledged")
	}
	assertPublicSnapshot(t, r.root, before)
	if journalExists(t, r) {
		t.Fatal("known permission failure published mutation intent")
	}
	if id, err := r.CurrentID(); err != nil || id != head {
		t.Fatalf("permission failure changed HEAD: %s %v", id, err)
	}
	if err = os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("after"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(parent, "tracked")); err != nil || string(data) != "owned target" {
		t.Fatalf("retry after operator fixture permission repair: %q %v", data, err)
	}
}
