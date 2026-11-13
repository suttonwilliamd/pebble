//go:build linux || darwin

package repository

import (
	"errors"
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
	t.Run("read-only-blocking-directory", func(t *testing.T) {
		r, err := Init(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		committed(t, r, "empty")
		if err = r.CreateBranch("empty"); err != nil {
			t.Fatal(err)
		}
		write(t, r.root, "node", []byte("preserved file revision"))
		committed(t, r, "file")
		if err = r.CreateBranch("file"); err != nil {
			t.Fatal(err)
		}
		if err = r.Checkout("empty"); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(r.root, "node")
		if err = os.Mkdir(path, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0755) })
		probe := filepath.Join(path, "permission-probe")
		f, e := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			_ = f.Close()
			_ = os.Remove(probe)
			t.Fatal("fixture does not provide real unprivileged directory write denial")
		}
		if !errors.Is(e, os.ErrPermission) {
			t.Fatalf("directory write probe: %v", e)
		}
		before := publicSnapshot(t, r.root)
		head, err := r.CurrentID()
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Checkout("file"); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("cross-parent retained directory permission not refused: %v", err)
		}
		assertPublicSnapshot(t, r.root, before)
		if journalExists(t, r) {
			t.Fatal("known retained-directory permission failure published intent")
		}
		if id, e := r.CurrentID(); e != nil || id != head {
			t.Fatalf("permission refusal changed HEAD: %s %v", id, e)
		}
		// Only the operator repairs this disposable fixture; the engine must
		// never chmod an original directory to manufacture rename capability.
		if err = os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err = r.Checkout("file"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("retry target type: %v", err)
		}
		if data, e := os.ReadFile(path); e != nil || string(data) != "preserved file revision" {
			t.Fatalf("retry/history bytes: %q %v", data, e)
		}
		if changes, e := r.Status(); e != nil || len(changes) != 0 {
			t.Fatalf("retry status: %v %v", changes, e)
		}
		if _, err = r.Verify(); err != nil {
			t.Fatal(err)
		}
	})
}
