//go:build linux || darwin

package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func requireRecoveryUnprivileged(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	if os.Getenv("PEBBLE_REQUIRE_UNPRIVILEGED") == "1" {
		t.Fatal("automatic permission recovery requires an actual non-root process")
	}
	t.Skip("root is not permission-failure evidence")
}

func TestFilesystemAutomaticRecoveryParentUnlinkAndReplacement(t *testing.T) {
	for _, which := range []string{"unlinked", "replaced"} {
		t.Run(which, func(t *testing.T) {
			r := mutationFixture(t)
			head, err := r.GetHead()
			if err != nil {
				t.Fatal(err)
			}
			removed := false
			parent := filepath.Join(r.root, "dir")
			r.fault = func(stage, path string) error {
				if stage == "retain-renamed" && !removed {
					removed = true
					// The pinned source directory is now empty. Unlink really
					// succeeds on POSIX; this is not a synthetic callback error.
					if err := os.Remove(parent); err != nil {
						return err
					}
					if which == "replaced" {
						if err := os.Mkdir(parent, 0755); err != nil {
							return err
						}
						return os.WriteFile(filepath.Join(parent, "tracked"), []byte("new parent and leaf belong to external editor"), 0644)
					}
				}
				return nil
			}
			if err = r.Checkout("after"); err == nil || !removed {
				t.Fatalf("native parent unlink was not detected: performed=%t err=%v", removed, err)
			}
			r.fault = nil
			public := publicSnapshot(t, r.root)
			journalID, err := filesystemIdentity(r.journalPath())
			if err != nil {
				t.Fatal(err)
			}
			receipt := automaticAbort(t, r, head)
			preservedVersion(t, receipt, "dir/tracked", "original", []byte("owned original"))
			retained := preservedVersion(t, receipt, "dir/tracked", "retained-original", []byte("owned original"))
			if id, err := filesystemIdentity(receipt.Directory); err != nil || id != journalID {
				t.Fatalf("journal inode was not retained by public placement: %s %v", id, err)
			}
			if _, err := os.Lstat(retained); err != nil {
				t.Fatal(err)
			}
			assertReconciledPublic(t, r, public, receipt)
			if which == "unlinked" {
				if _, err = os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("recovery recreated an unowned/unlinked parent: %v", err)
				}
			}
			root := r.root
			for i := 0; i < 2; i++ {
				r, err = Open(root)
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertReconciledPublic(t, r, public, receipt)
			}
		})
	}
}

func TestFilesystemAutomaticRecoveryPermissionLoss(t *testing.T) {
	requireRecoveryUnprivileged(t)
	f := pauseCheckout(t, "retain-published")
	foreign := []byte("new external bytes force safe abort")
	write(t, f.r.root, "removed", foreign)
	originalID, err := filesystemIdentity(filepath.Join(f.r.journalPath(), retainedName(0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.r.root, 0755) })
	if err = os.Chmod(f.r.root, 0555); err != nil {
		t.Fatal(err)
	}
	public := publicSnapshot(t, f.r.root)
	if err = f.r.Recover(context.Background()); err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("required public-name repair did not report actual OS write denial: %v", err)
	}
	if !journalExists(t, f.r) {
		t.Fatal("write denial retired incomplete public-name repair")
	}
	assertPublicSnapshot(t, f.r.root, public)
	if id, err := filesystemIdentity(filepath.Join(f.r.journalPath(), retainedName(0))); err != nil || id != originalID {
		t.Fatalf("write denial moved/lost retained original: %s %v", id, err)
	}
	// Explicit fixture operator restores access, not filenames. Production
	// neither chmods the external source nor retries in a background loop.
	if err = os.Chmod(f.r.root, 0755); err != nil {
		t.Fatal(err)
	}
	before := publicSnapshot(t, f.r.root)
	reopenRecovery(t, &f)
	receipt := automaticAbort(t, f.r, "heads/before")
	preservedVersion(t, receipt, "a.bin", "original", f.old["a.bin"])
	if id, err := filesystemIdentity(filepath.Join(f.r.root, "a.bin")); err != nil || id != originalID {
		t.Fatalf("safe automatic return did not preserve original inode: %s %v", id, err)
	}
	assertReconciledPublic(t, f.r, before, receipt, "a.bin")
	if got, err := os.ReadFile(filepath.Join(f.r.root, "removed")); err != nil || string(got) != string(foreign) {
		t.Fatalf("external public bytes changed across permission recovery: %q %v", got, err)
	}
	for i := 0; i < 2; i++ {
		reopenRecovery(t, &f)
		if err = f.r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertReconciledPublic(t, f.r, before, receipt, "a.bin")
	}
}

func TestFilesystemAutomaticRecoveryAfterRenameTypes(t *testing.T) {
	for _, point := range []string{"install-renamed", "directory-renamed", "repair-restore-renamed"} {
		for _, kind := range []string{"file", "directory", "link"} {
			t.Run(point+"/"+kind, func(t *testing.T) {
				r, err := Init(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if kind == "link" && !requireFixtureLinks(t, r.root) {
					t.Skip("native links unavailable; not performed-link evidence")
				}
				name := "dir/tracked"
				if point == "directory-renamed" {
					name = "node"
				}
				write(t, r.root, name, []byte("owned original bytes"))
				write(t, r.root, "guard", []byte("unchanged guarded leaf"))
				beforeID := committed(t, r, "before")
				if err = r.CreateBranch("before"); err != nil {
					t.Fatal(err)
				}
				if point == "directory-renamed" {
					if err = os.Remove(filepath.Join(r.root, name)); err != nil {
						t.Fatal(err)
					}
					write(t, r.root, name+"/child", []byte("requested directory child"))
				} else {
					write(t, r.root, name, []byte("owned target bytes"))
				}
				committed(t, r, "after")
				if err = r.CreateBranch("after"); err != nil {
					t.Fatal(err)
				}
				if err = r.Checkout("before"); err != nil {
					t.Fatal(err)
				}
				if point == "repair-restore-renamed" {
					r.fault = func(stage, path string) error {
						if stage == "retain-published" {
							return errors.New("interrupt before install")
						}
						return nil
					}
					if err = r.Checkout("after"); err == nil {
						t.Fatal("fixture did not retain original")
					}
					write(t, r.root, "guard", []byte("later external edit initiates abort"))
				}
				performed := false
				var expected map[string]string
				r.fault = func(stage, path string) error {
					if stage != point || performed {
						return nil
					}
					performed = true
					if err := os.Rename(path, filepath.Join(r.root, "saved-engine-version")); err != nil {
						return err
					}
					switch kind {
					case "file":
						if err := os.WriteFile(path, []byte("newer public file bytes"), 0644); err != nil {
							return err
						}
					case "directory":
						if err := os.Mkdir(path, 0755); err != nil {
							return err
						}
						if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("newer public directory child"), 0644); err != nil {
							return err
						}
					case "link":
						if err := createLink("foreign-MiXeD-dangling", path, false); err != nil {
							return err
						}
					}
					expected = publicSnapshot(t, r.root)
					return nil
				}
				if point == "repair-restore-renamed" {
					err = r.Recover(context.Background())
				} else {
					err = r.Checkout("after")
				}
				if err == nil || !performed || expected == nil {
					t.Fatalf("actual post-rename %s substitution did not fail safely: performed=%t err=%v", kind, performed, err)
				}
				r.fault = nil
				// A newer public inode must not be reversed into metadata.
				assertPublicSnapshot(t, r.root, expected)
				if id, err := r.CurrentID(); err != nil || id != beforeID {
					t.Fatalf("post-rename substitution advanced HEAD: %s %v", id, err)
				}
				receipt := automaticAbort(t, r, "heads/before")
				preservedVersion(t, receipt, name, "original", []byte("owned original bytes"))
				assertReconciledPublic(t, r, expected, receipt)
				for i := 0; i < 2; i++ {
					r, err = Open(r.root)
					if err != nil {
						t.Fatal(err)
					}
					if err = r.Recover(context.Background()); err != nil {
						t.Fatal(err)
					}
					assertPublicSnapshot(t, r.root, expected)
				}
				t.Logf("performed post-rename %s substitution at %s; public identity/type/target/bytes retained and abort converged", kind, point)
			})
		}
	}
}
