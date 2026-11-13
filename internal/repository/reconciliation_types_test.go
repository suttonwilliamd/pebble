package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func recoveryNativeLink(t *testing.T, path string) (string, bool) {
	t.Helper()
	a, err := anchorPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	link, err := a.readLink()
	closeErr := a.close()
	if err != nil || closeErr != nil {
		t.Fatalf("read native literal recovery link: %v %v", err, closeErr)
	}
	return link.target, link.directory
}

func TestAutomaticRecoveryLiteralLinkPreimages(t *testing.T) {
	for _, directory := range []bool{false, true} {
		for _, stage := range []string{"journal-published", "retain-published", "install-published"} {
			t.Run(fmt.Sprintf("directory-hint=%t/%s", directory, stage), func(t *testing.T) {
				r, err := Init(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if !requireFixtureLinks(t, r.root) {
					t.Skip("native symbolic-link creation unavailable; not native recovery evidence")
				}
				node := filepath.Join(r.root, "node")
				if err = createLink("tracked-dangling", node, directory); err != nil {
					t.Fatal(err)
				}
				head := committed(t, r, "native link history")
				if err = os.Remove(node); err != nil {
					t.Fatal(err)
				}
				if err = createLink("dirty-MiXeD-東京-Σ", node, directory); err != nil {
					t.Fatal(err)
				}
				dirtyTarget, dirtyDirectory := recoveryNativeLink(t, node)
				interruptWorktree(t, r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_FILE=node", "PEBBLE_TEST_WORKTREE_STAGE="+stage)
				if err = os.Remove(node); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err = createLink("later-external-dangling", node, !directory); err != nil {
					t.Fatal(err)
				}
				public := publicSnapshot(t, r.root)
				r, err = Open(r.root)
				if err != nil {
					t.Fatal(err)
				}
				receipt := automaticAbort(t, r, "heads/main")
				assertReconciledPublic(t, r, public, receipt)
				original := ""
				for _, version := range receipt.Versions {
					if version.Path == "node" && version.Role == "original" {
						original = version.Location
						if version.Changed {
							t.Fatal("newly archived literal original changed")
						}
					}
				}
				if original == "" {
					t.Fatal("receipt did not expose dirty native link preimage")
				}
				if target, hint := recoveryNativeLink(t, original); target != dirtyTarget || hint != dirtyDirectory {
					t.Fatalf("archive normalized/dereferenced native original: %q/%t want %q/%t", target, hint, dirtyTarget, dirtyDirectory)
				}
				archive := publicSnapshot(t, receipt.Directory)
				for i := 0; i < 2; i++ {
					r, err = Open(r.root)
					if err != nil {
						t.Fatal(err)
					}
					if err = r.Recover(context.Background()); err != nil {
						t.Fatal(err)
					}
					assertPublicSnapshot(t, r.root, public)
					assertPublicSnapshot(t, receipt.Directory, archive)
					if got, err := r.CurrentID(); err != nil || got != head {
						t.Fatalf("aborted link restore changed HEAD: %s %v", got, err)
					}
				}
			})
		}
	}
}

func TestAutomaticRecoveryDirectoryTransition(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer-public-directory=%t", occupied), func(t *testing.T) {
			r, err := Init(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "node/child", []byte("original child bytes"))
			write(t, r.root, "anchor", []byte("original anchor"))
			beforeID := committed(t, r, "directory before transition")
			if err = r.CreateBranch("before"); err != nil {
				t.Fatal(err)
			}
			if err = os.RemoveAll(filepath.Join(r.root, "node")); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "node", []byte("requested regular replacement"))
			committed(t, r, "regular target")
			if err = r.CreateBranch("after"); err != nil {
				t.Fatal(err)
			}
			if err = r.Checkout("before"); err != nil {
				t.Fatal(err)
			}
			dirID, err := filesystemIdentity(filepath.Join(r.root, "node"))
			if err != nil {
				t.Fatal(err)
			}
			childID, err := filesystemIdentity(filepath.Join(r.root, "node", "child"))
			if err != nil {
				t.Fatal(err)
			}
			interruptWorktree(t, r.root, "PEBBLE_TEST_WORKTREE_BRANCH=after", "PEBBLE_TEST_WORKTREE_STAGE=retain-directory-published")
			write(t, r.root, "anchor", []byte("concurrent external anchor"))
			if occupied {
				write(t, r.root, "node/foreign", []byte("newer public directory and bytes"))
			}
			public := publicSnapshot(t, r.root)
			receipt := automaticAbort(t, r, "heads/before")
			preservedVersion(t, receipt, "node/child", "original", []byte("original child bytes"))
			if occupied {
				assertReconciledPublic(t, r, public, receipt)
				preservedVersion(t, receipt, "node/child", "retained-original", []byte("original child bytes"))
			} else {
				assertReconciledPublic(t, r, public, receipt, "node", "node/child")
				for name, expected := range map[string]string{"node": dirID, "node/child": childID} {
					if id, err := filesystemIdentity(filepath.Join(r.root, filepath.FromSlash(name))); err != nil || id != expected {
						t.Fatalf("automatic native directory/child restoration %s: %s want %s: %v", name, id, expected, err)
					}
				}
			}
			public = publicSnapshot(t, r.root)
			for i := 0; i < 2; i++ {
				r, err = Open(r.root)
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertPublicSnapshot(t, r.root, public)
				if got, err := r.CurrentID(); err != nil || got != beforeID {
					t.Fatalf("directory abort HEAD: %s %v", got, err)
				}
			}
		})
	}
}

func TestAutomaticRecoveryUntouchedDeletion(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node", []byte("old changed leaf"))
	write(t, r.root, "untouched", []byte("unchanged bytes stay in immutable history"))
	beforeID := committed(t, r, "before")
	if err = r.CreateBranch("before"); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node", []byte("target changed leaf"))
	committed(t, r, "after")
	if err = r.Checkout("before"); err != nil {
		t.Fatal(err)
	}
	r.fault = func(stage, path string) error {
		if stage == "journal-published" {
			return fmt.Errorf("interruption after intent")
		}
		return nil
	}
	if err = r.Checkout("main"); err == nil {
		t.Fatal("fixture did not stop after intent")
	}
	r.fault = nil
	if err = os.Remove(filepath.Join(r.root, "untouched")); err != nil {
		t.Fatal(err)
	}
	public := publicSnapshot(t, r.root)
	receipt := automaticAbort(t, r, "heads/before")
	assertReconciledPublic(t, r, public, receipt)
	for i := 0; i < 2; i++ {
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertPublicSnapshot(t, r.root, public)
		if got, err := r.CurrentID(); err != nil || got != beforeID {
			t.Fatalf("external deletion rewrote pinned history: %s %v", got, err)
		}
	}
}
