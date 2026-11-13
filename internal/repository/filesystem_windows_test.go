package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFilesystemRejectsJunctionRecovery(t *testing.T) {
	r := mutationFixture(t)
	head, err := r.CurrentID()
	if err != nil {
		t.Fatal(err)
	}
	stopped := errors.New("published intent")
	r.fault = func(stage, path string) error {
		if stage == "journal-published" {
			return stopped
		}
		return nil
	}
	if err = r.Checkout("after"); !errors.Is(err, stopped) {
		t.Fatal(err)
	}
	r.fault = nil
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "tracked")
	if err = os.WriteFile(outsideFile, []byte("outside junction data"), 0644); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(r.root, "dir")
	if err = os.Rename(junction, filepath.Join(r.root, "saved-directory")); err != nil {
		t.Fatal(err)
	}
	// Junction creation requires no new OS setting/privilege; failure is reported,
	// never retried with elevation, Developer Mode or an ACL change.
	cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		if os.Getenv("PEBBLE_REQUIRE_JUNCTION") == "1" {
			t.Fatalf("required disposable junction fixture unavailable: %v\n%s", err, out)
		}
		t.Logf("junction capability unavailable; no junction-rejection proof claimed: %v\n%s", err, out)
		return
	}
	t.Cleanup(func() { _ = os.Remove(junction) })
	beforeIdentity, err := filesystemIdentity(junction)
	if err != nil {
		t.Fatal(err)
	}
	beforeTarget, err := os.Readlink(junction)
	if err != nil {
		t.Fatal(err)
	}
	receipt := automaticAbort(t, r, "heads/before")
	preservedVersion(t, receipt, "dir/tracked", "original", []byte("owned original"))
	for i := 0; i < 2; i++ {
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if after, err := filesystemIdentity(junction); err != nil || after != beforeIdentity {
		t.Fatalf("junction public identity changed: %s %v", after, err)
	}
	if target, err := os.Readlink(junction); err != nil || target != beforeTarget {
		t.Fatalf("junction public target changed: %s %v", target, err)
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "outside junction data" {
		t.Fatalf("outside target modified: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(r.root, "saved-directory", "tracked")); err != nil || string(data) != "owned original" {
		t.Fatalf("original public directory modified: %q %v", data, err)
	}
	if id, err := r.CurrentID(); err != nil || id != head {
		t.Fatalf("HEAD changed: %s %v", id, err)
	}
	if journalExists(t, r) {
		t.Fatal("no-follow automatic abort left an active intent")
	}
}
