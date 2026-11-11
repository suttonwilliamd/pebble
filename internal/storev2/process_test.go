package storev2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run only in a child of these tests. os.Exit deliberately bypasses all deferred
// cleanup, simulating process interruption, NOT power loss or storage failure.
func TestStoreProcessHelper(t *testing.T) {
	mode := os.Getenv("PEBBLE_V2_TEST_MODE")
	if mode == "" {
		return
	}
	s, err := Open(os.Getenv("PEBBLE_V2_TEST_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	byName, _ := corpus(t)
	ctx := context.Background()
	if mode == "locked" {
		if err = s.Put(ctx, goldenID(t, byName["chunk-a"]), bytes.NewReader(goldenBytes(t, byName["chunk-a"]))); !errors.Is(err, ErrLocked) {
			t.Fatal("cross-process lock not enforced:", err)
		}
		return
	}
	stage := os.Getenv("PEBBLE_V2_TEST_STAGE")
	s.fault = func(point, path string) error {
		extension := ".obj"
		if mode == "ref" {
			extension = ".ref"
		}
		if point == stage && filepath.Ext(path) == extension {
			os.Exit(73)
		}
		return nil
	}
	if mode == "object" {
		err = s.Put(ctx, goldenID(t, byName["chunk-a"]), bytes.NewReader(goldenBytes(t, byName["chunk-a"])))
	} else if mode == "ref" {
		old, next := goldenID(t, byName["commit-old"]), goldenID(t, byName["commit-new"])
		err = s.UpdateRef(ctx, "heads/main", &old, next)
	} else {
		t.Fatal("unknown helper mode")
	}
	t.Fatal("interruption boundary not reached:", err)
}

func process(t *testing.T, root, mode, stage string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestStoreProcessHelper$")
	cmd.Env = append(os.Environ(), "PEBBLE_V2_TEST_MODE="+mode, "PEBBLE_V2_TEST_ROOT="+root, "PEBBLE_V2_TEST_STAGE="+stage)
	return cmd.CombinedOutput()
}

func TestWriterLockAcrossProcesses(t *testing.T) {
	s := newStore(t)
	unlock, err := s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}()
	output, err := process(t, s.root, "locked", "")
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
}

func TestProcessInterruptionKeepsCompletePublicationAndStaleLock(t *testing.T) {
	byName, _ := corpus(t)
	ctx := context.Background()
	for _, mode := range []string{"object", "ref"} {
		for _, stage := range []string{"before-publish", "after-publish"} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				s := newStore(t)
				old, next := goldenID(t, byName["commit-old"]), goldenID(t, byName["commit-new"])
				if mode == "ref" {
					putGoldens(t, s, byName, "commit-new")
					if err := s.UpdateRef(ctx, "heads/main", nil, old); err != nil {
						t.Fatal(err)
					}
				}
				output, err := process(t, s.root, mode, stage)
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("child did not interrupt at boundary: %v\n%s", err, output)
				}
				reopened, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				chunk := goldenID(t, byName["chunk-a"])
				wire := goldenBytes(t, byName["chunk-a"])
				if mode == "ref" {
					want := old
					if stage == "after-publish" {
						want = next
					}
					assertRef(t, reopened, "heads/main", want)
				} else if stage == "before-publish" {
					assertAbsent(t, reopened.objectPath(chunk))
				} else {
					var restored bytes.Buffer
					if err = reopened.Copy(ctx, chunk, &restored); err != nil || !bytes.Equal(restored.Bytes(), wire) {
						t.Fatal("incomplete published object after interruption:", err)
					}
				}
				if err = reopened.Put(ctx, chunk, bytes.NewReader(wire)); !errors.Is(err, ErrLocked) {
					t.Fatal("stale lock silently removed:", err)
				}
				// CombinedOutput waited for the child. This is an explicit test
				// recovery decision, never automatic production stale-lock repair.
				if err = os.Remove(filepath.Join(s.meta, "write.lock")); err != nil {
					t.Fatal(err)
				}
				if mode == "ref" {
					if err = reopened.UpdateRef(ctx, "heads/main", &old, next); err != nil {
						t.Fatal("retry after confirmed process exit:", err)
					}
					assertRef(t, reopened, "heads/main", next)
				} else if err = reopened.Put(ctx, chunk, bytes.NewReader(wire)); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
