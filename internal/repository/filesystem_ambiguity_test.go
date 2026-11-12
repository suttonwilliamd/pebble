//go:build linux || darwin

package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemNativeRenameAmbiguity(t *testing.T) {
	if os.Geteuid() == 0 {
		if os.Getenv("PEBBLE_REQUIRE_UNPRIVILEGED") == "1" {
			t.Fatal("rollback permission proof requires a non-root process")
		}
		t.Skip("root cannot qualify permission-blocked rollback")
	}
	for _, which := range []string{"intervening-entry", "permission-change"} {
		t.Run(which, func(t *testing.T) {
			r := mutationFixture(t)
			head, err := r.CurrentID()
			if err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(r.root, "dir")
			source := filepath.Join(parent, "tracked")
			t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
			replaced, intervened := false, false
			r.fault = func(stage, path string) error {
				if stage == "retain-rename" && !replaced {
					replaced = true
					if err := os.Rename(source, filepath.Join(r.root, "saved-original")); err != nil {
						return err
					}
					return os.WriteFile(source, []byte("foreign first version"), 0644)
				}
				if stage == "retain-renamed" && replaced && !intervened {
					intervened = true
					if which == "intervening-entry" {
						return os.WriteFile(source, []byte("foreign later public version"), 0644)
					}
					return os.Chmod(parent, 0555)
				}
				return nil
			}
			err = r.Checkout("after")
			if err == nil || !strings.Contains(err.Error(), "cannot restore source directory entry") {
				t.Fatalf("rollback ambiguity not surfaced: %v", err)
			}
			if !replaced || !intervened {
				t.Fatal("native rename/rollback windows not reached")
			}
			r.fault = nil
			retained := filepath.Join(r.journalPath(), retainedName(0))
			if !strings.Contains(err.Error(), source) || !strings.Contains(err.Error(), retained) {
				t.Fatalf("operator recovery locations missing: %v", err)
			}
			if data, e := os.ReadFile(retained); e != nil || string(data) != "foreign first version" {
				t.Fatalf("first foreign version lost: %q %v", data, e)
			}
			if data, e := os.ReadFile(filepath.Join(r.root, "saved-original")); e != nil || string(data) != "owned original" {
				t.Fatalf("original version lost: %q %v", data, e)
			}
			if which == "intervening-entry" {
				if data, e := os.ReadFile(source); e != nil || string(data) != "foreign later public version" {
					t.Fatalf("intervening public version overwritten: %q %v", data, e)
				}
			} else {
				if _, e := os.Lstat(source); !os.IsNotExist(e) {
					t.Fatalf("permission-blocked source state misreported: %v", e)
				}
				t.Logf("observable limitation: public %s remains absent; unexpected version retained at %s; operator must preserve all versions and resolve permissions/ownership", source, retained)
			}
			if id, e := r.CurrentID(); e != nil || id != head {
				t.Fatalf("HEAD advanced on rollback ambiguity: %s %v", id, e)
			}
			public := publicSnapshot(t, r.root)
			before := fixtureSnapshot(t, r.root)
			if err = r.Recover(context.Background()); err == nil {
				t.Fatal("recovery treated ambiguity as overwrite authority")
			}
			assertPublicSnapshot(t, r.root, public)
			after := fixtureSnapshot(t, r.root)
			for path, want := range before {
				if after[path] != want {
					t.Fatalf("failed recovery changed preserved version %s", path)
				}
			}
		})
	}
}
