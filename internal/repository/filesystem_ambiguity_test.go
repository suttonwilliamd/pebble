//go:build linux || darwin

package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemNativeRenameAmbiguity(t *testing.T) {
	requireRecoveryUnprivileged(t)
	for _, which := range []string{"intervening-entry", "permission-change"} {
		t.Run(which, func(t *testing.T) {
			r := mutationFixture(t)
			head, err := r.GetHead()
			if err != nil {
				t.Fatal(err)
			}
			beforeID, err := r.CurrentID()
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
			if err = r.Checkout("after"); err == nil || !replaced || !intervened {
				t.Fatalf("performed native substitution/reversal block not detected: replaced=%t intervened=%t err=%v", replaced, intervened, err)
			}
			r.fault = nil
			retained := filepath.Join(r.journalPath(), retainedName(0))
			displacedID, err := filesystemIdentity(retained)
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(retained); err != nil || string(data) != "foreign first version" {
				t.Fatalf("displaced version lost before automatic recovery: %q %v", data, err)
			}
			public := publicSnapshot(t, r.root)
			if which == "permission-change" {
				if err = r.Recover(context.Background()); err == nil || !errors.Is(err, os.ErrPermission) {
					t.Fatalf("required canonical repair did not remain pending on real write denial: %v", err)
				}
				if !journalExists(t, r) {
					t.Fatal("permission-blocked canonical repair was falsely completed by archival alone")
				}
				assertPublicSnapshot(t, r.root, public)
				// Fixture operator restores permissions only. No filename repair.
				if err = os.Chmod(parent, 0755); err != nil {
					t.Fatal(err)
				}
				public = publicSnapshot(t, r.root)
			}
			receipt := automaticAbort(t, r, head)
			preservedVersion(t, receipt, "dir/tracked", "original", []byte("owned original"))
			preservedVersion(t, receipt, "dir/tracked", "displaced", []byte("foreign first version"))
			if which == "intervening-entry" {
				if data, err := os.ReadFile(source); err != nil || string(data) != "foreign later public version" {
					t.Fatalf("newer canonical version was overwritten: %q %v", data, err)
				}
				assertReconciledPublic(t, r, public, receipt)
			} else {
				if data, err := os.ReadFile(source); err != nil || string(data) != "foreign first version" {
					t.Fatalf("vacant public source was not automatically repaired: %q %v", data, err)
				}
				if id, err := filesystemIdentity(source); err != nil || id != displacedID {
					t.Fatalf("canonical repair did not return actual displaced inode: %s %v", id, err)
				}
				assertReconciledPublic(t, r, public, receipt, "dir/tracked")
			}
			if data, err := os.ReadFile(filepath.Join(r.root, "saved-original")); err != nil || string(data) != "owned original" {
				t.Fatalf("original public saved version changed: %q %v", data, err)
			}
			if id, err := r.CurrentID(); err != nil || id != beforeID {
				t.Fatalf("automatic abort advanced HEAD: %s %v", id, err)
			}
			exact := publicSnapshot(t, r.root)
			root := r.root
			for i := 0; i < 2; i++ {
				r, err = Open(root)
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertPublicSnapshot(t, r.root, exact)
			}
		})
	}
}
