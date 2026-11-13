package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemLinkDirectoryTransitions(t *testing.T) {
	for _, step := range []struct{ branch, point string }{{"directory", "retain-published"}, {"link", "retain-directory-published"}, {"file", "install-published"}} {
		t.Run(step.branch, func(t *testing.T) {
			r := transitionFixture(t)
			if !requireFixtureLinks(t, r.root) {
				return
			}
			outside := filepath.Join(t.TempDir(), "precious")
			if err := os.WriteFile(outside, []byte("outside unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := r.Checkout("file"); err != nil {
				t.Fatal(err)
			}
			// Commit on the link branch: advancing "file" here would turn the
			// link-to-file case into a legitimate no-op with no install seam.
			if err := r.CreateBranch("link"); err != nil {
				t.Fatal(err)
			}
			if err := r.Checkout("link"); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(r.root, "node")); err != nil {
				t.Fatal(err)
			}
			if err := createLink(outside, filepath.Join(r.root, "node"), false); err != nil {
				t.Fatal(err)
			}
			committed(t, r, "link")
			if step.branch == "link" {
				if err := r.Checkout("directory"); err != nil {
					t.Fatal(err)
				}
			}
			stop := errors.New("interrupted native link transition")
			r.fault = func(stage, path string) error {
				if stage == step.point {
					return stop
				}
				return nil
			}
			if err := r.Checkout(step.branch); !errors.Is(err, stop) {
				t.Fatalf("transition did not reach %s: %v", step.point, err)
			}
			var err error
			r, err = Open(r.root)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err = r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if head, e := r.GetHead(); e != nil || head != "heads/"+step.branch {
				t.Fatalf("recovery HEAD: %s %v", head, e)
			}
			if step.branch == "link" {
				link, err := readLink(filepath.Join(r.root, "node"))
				if err != nil || link.target != outside {
					t.Fatalf("literal restored link: %+v %v", link, err)
				}
			} else {
				info, e := os.Lstat(filepath.Join(r.root, "node"))
				if e != nil || step.branch == "file" && !info.Mode().IsRegular() || step.branch == "directory" && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
					t.Fatalf("recovered target type %s: %v", step.branch, e)
				}
				assertTransition(t, r, step.branch == "directory")
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != "outside unchanged" {
				t.Fatalf("traversed external target: %q %v", got, err)
			}
			if changes, err := r.Status(); err != nil || len(changes) != 0 {
				t.Fatalf("recovered transition status: %v %v", changes, err)
			}
			if _, err = r.Verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestFilesystemLinkRestoreRecoveryPreservesLaterEdit(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !requireFixtureLinks(t, r.root) {
		return
	}
	if err = createLink("literal-dangling", filepath.Join(r.root, "node"), false); err != nil {
		t.Fatal(err)
	}
	head := committed(t, r, "link history")
	if err = os.Remove(filepath.Join(r.root, "node")); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node", []byte("authorized initial discard"))
	interruptWorktree(t, r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_FILE=node", "PEBBLE_TEST_WORKTREE_STAGE=retain-published")
	write(t, r.root, "node", []byte("foreign edit after interruption"))
	before := publicSnapshot(t, r.root)
	r, err = Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	currentHead, err := r.GetHead()
	if err != nil {
		t.Fatal(err)
	}
	receipt := automaticAbort(t, r, currentHead)
	assertReconciledPublic(t, r, before, receipt)
	preservedVersion(t, receipt, "node", "original", []byte("authorized initial discard"))
	if id, err := r.CurrentID(); err != nil || id != head {
		t.Fatalf("HEAD changed: %s %v", id, err)
	}
	for i := 0; i < 2; i++ {
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertPublicSnapshot(t, r.root, before)
	}
	if got, err := os.ReadFile(filepath.Join(r.root, "node")); err != nil || string(got) != "foreign edit after interruption" {
		t.Fatalf("automatic recovery changed later public edit: %q %v", got, err)
	}
}
