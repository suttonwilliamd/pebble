package repository

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/processlock"
)

func TestStagingInterruptedCommit(t *testing.T) {
	for _, point := range []string{"commit-prepared", "commit-published"} {
		t.Run(point, func(t *testing.T) {
			r, err := Init(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "selected", []byte("base"))
			before := committed(t, r, "base")
			a := []byte{0, 255, 'A', '\r', '\n', 0}
			b := []byte{255, 0, 'B', '\n'}
			write(t, r.root, "selected", a)
			if err = r.Stage([]string{"selected"}); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "selected", b)
			write(t, r.root, "unselected", []byte("must not enter commit"))
			cmd := exec.Command(os.Args[0], "-test.run=^TestStagingProcessChild$", "-test.v")
			cmd.Env = append(os.Environ(), "PEBBLE_STAGING_ROOT="+r.root, "PEBBLE_STAGING_POINT="+point)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("child did not interrupt: %s", out)
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 73 {
				t.Fatalf("child interruption: %v\n%s", err, out)
			}
			r, err = Open(r.root)
			if err != nil {
				t.Fatal(err)
			}
			id, err := r.CurrentID()
			if err != nil {
				t.Fatal(err)
			}
			if point == "commit-prepared" {
				if id != before {
					t.Fatal("prepared commit advanced HEAD")
				}
				id, err = r.Commit("retry exact A", fixtureIdentity, false)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if id == before {
					t.Fatal("published commit missing")
				}
				if _, err = r.Commit("must not duplicate", fixtureIdentity, false); err == nil {
					t.Fatal("consumed staged revision committed twice")
				}
			}
			c, err := r.GetCommit(id)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := r.store.ReadTree(context.Background(), c.Tree)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Path != "selected" {
				t.Fatalf("unstaged content swept into commit: %v", entries)
			}
			var out bytes.Buffer
			if err = r.store.CopyFile(context.Background(), entries[0].Manifest, &out); err != nil || !bytes.Equal(out.Bytes(), a) {
				t.Fatalf("staged A changed: %x %v", out.Bytes(), err)
			}
			if data, err := os.ReadFile(filepath.Join(r.root, "selected")); err != nil || !bytes.Equal(data, b) {
				t.Fatalf("working B changed: %x %v", data, err)
			}
			changes, err := r.Status()
			if err != nil {
				t.Fatal(err)
			}
			if len(changes) != 2 || changes[0] != (Change{" M", "selected"}) || changes[1] != (Change{"??", "unselected"}) {
				t.Fatalf("post-interruption index: %v", changes)
			}
		})
	}
}
func TestStagingProcessChild(t *testing.T) {
	root := os.Getenv("PEBBLE_STAGING_ROOT")
	if root == "" {
		return
	}
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r.fault = func(point, path string) error {
		if point == os.Getenv("PEBBLE_STAGING_POINT") {
			os.Exit(73)
		}
		return nil
	}
	if _, err = r.Commit("interrupted exact A", fixtureIdentity, false); err != nil {
		t.Fatal(err)
	}
	t.Fatal("requested commit boundary not reached")
}
func TestStagingFailsClosed(t *testing.T) {
	for _, kind := range []string{"missing", "malformed", "stale", "checkout", "worktree.guard", "write.guard", "worktree-journal"} {
		t.Run(kind, func(t *testing.T) {
			r, err := Init(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "selected", []byte("base"))
			before := committed(t, r, "base")
			if err = r.CreateBranch("before"); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "selected", []byte("staged A"))
			if err = r.Stage([]string{"selected"}); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "selected", []byte("working B"))
			write(t, r.root, "unselected", []byte("untracked"))
			path := filepath.Join(r.root, ".pebble", "index")
			var unlock func() error
			switch kind {
			case "missing":
				err = os.Remove(path)
			case "malformed":
				err = os.WriteFile(path, []byte("not a valid staging record"), 0600)
			case "stale":
				before = frozenSnapshot(t, r, "external advance behind index")
			case "worktree.guard", "write.guard":
				unlock, err = processlock.Acquire(filepath.Join(r.root, ".pebble", kind))
			case "worktree-journal":
				err = os.Mkdir(filepath.Join(r.root, ".pebble", kind), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if unlock != nil {
				defer unlock()
			}
			public := publicSnapshot(t, r.root)
			if kind == "checkout" {
				if err = r.Checkout("before"); err == nil {
					t.Fatal("checkout discarded staged A")
				}
			} else {
				if _, err = r.Commit("must not sweep", fixtureIdentity, true); err == nil {
					t.Fatal("invalid or locked index accepted by --all")
				}
				if err = r.Stage([]string{"unselected"}); err == nil {
					t.Fatal("invalid or locked index overwritten by add")
				}
			}
			after, err := r.CurrentID()
			if err != nil || after != before {
				t.Fatalf("failed operation changed HEAD: %s %v", after, err)
			}
			assertPublicSnapshot(t, r.root, public)
		})
	}
}
func TestStagingIndexPublicationFailure(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "selected", []byte("base"))
	before := committed(t, r, "base")
	write(t, r.root, "selected", []byte("staged A"))
	if err = r.Stage([]string{"selected"}); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "selected", []byte("working B"))
	stopped := errors.New("interrupted index publication")
	r.fault = func(point, path string) error {
		if point == "index-publish" {
			return stopped
		}
		return nil
	}
	if err = r.Stage([]string{"selected"}); !errors.Is(err, stopped) {
		t.Fatal(err)
	}
	r.fault = nil
	id, err := r.Commit("A survives failed restage", fixtureIdentity, false)
	if err != nil {
		t.Fatal(err)
	}
	if id == before {
		t.Fatal("staged commit missing")
	}
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = r.store.CopyFile(context.Background(), entries[0].Manifest, &out); err != nil || out.String() != "staged A" {
		t.Fatalf("failed add changed acknowledged revision: %q %v", out.String(), err)
	}
}
func TestStagingOldConsumers(t *testing.T) {
	source := os.Getenv("PEBBLE_TEST_STAGING_OLD_SOURCE")
	if source == "" {
		if os.Getenv("PEBBLE_REQUIRE_STAGING_OLD_CONSUMER") == "1" {
			t.Fatal("required actual PB11 source unavailable")
		}
		t.Skip("native CI supplies frozen PB11 source")
	}
	if !strings.HasPrefix(filepath.Base(source), "pebble-pb12-old-") {
		t.Fatal("old source must be a generated fixture")
	}
	baseline, err := os.ReadFile(filepath.Join(source, "BASELINE"))
	if err != nil || strings.TrimSpace(string(baseline)) != "4f6c9aa256ba90f1185a4225519d7733bd10ef57" {
		t.Fatalf("incorrect old source %q %v", baseline, err)
	}
	repoBin := buildOldConsumer(t, source, "internal/repository", oldRepositoryConsumer, `unlock, err := processlock.Acquire(filepath.Join(r.root, ".pebble", "worktree.guard"))`)
	storeBin := buildOldConsumer(t, source, "internal/storev2", oldStoreConsumer, `unlock, err := processlock.Acquire(filepath.Join(s.meta, "write.guard"))`)
	for _, action := range []string{"held", "prelock-repository", "prelock-store", "active-worktree", "active-store"} {
		t.Run(action, func(t *testing.T) {
			r := oldConsumerFixture(t)
			write(t, r.root, "tracked", []byte("working bytes"))
			bin, mode, prelock := repoBin, action, false
			if strings.HasPrefix(action, "prelock-") {
				mode, prelock = "prelock", true
				if action == "prelock-store" {
					bin = storeBin
				}
			}
			child := startOldConsumer(t, bin, r.root, mode, prelock)
			if strings.HasPrefix(action, "active-") {
				if err = r.Stage([]string{"tracked"}); err == nil {
					t.Fatal("add bypassed active old writer")
				}
				child.resume(t, "COMPLETED_OLD")
				if err = r.Stage([]string{"tracked"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = r.Stage([]string{"tracked"}); err != nil {
					t.Fatal(err)
				}
				before := fixtureSnapshot(t, r.root)
				expected := "REJECTED_HELD"
				if prelock {
					expected = "REJECTED_PRELOCK"
				}
				child.resume(t, expected)
				after := fixtureSnapshot(t, r.root)
				if len(before) != len(after) {
					t.Fatal("old consumer changed staged repository")
				}
				for path, value := range before {
					if after[path] != value {
						t.Fatalf("old consumer changed %s", path)
					}
				}
			}
		})
	}
}
