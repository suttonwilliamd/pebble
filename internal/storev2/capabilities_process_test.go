package storev2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFilesystemEnableProcessHelper(t *testing.T) {
	root := os.Getenv("PEBBLE_ENABLE_ROOT")
	if root == "" {
		return
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	point := os.Getenv("PEBBLE_ENABLE_POINT")
	s.fault = func(stage, path string) error {
		if stage == point && path == filepath.Join(s.meta, "format") {
			os.Exit(73)
		}
		return nil
	}
	if err = s.EnableFilesystem(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Fatal("capability interruption point not reached")
}
func TestFilesystemEnablementProcessInterruption(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"file-sync", "before-publish", "namespace-sync", "after-publish"} {
		t.Run(point, func(t *testing.T) {
			s := oldMarkerStore(t)
			ctx := context.Background()
			manifest, err := s.IngestFile(ctx, bytes.NewReader([]byte("preserved historical bytes")), nil)
			if err != nil {
				t.Fatal(err)
			}
			working := filepath.Join(s.root, "precious")
			if err = os.WriteFile(working, []byte("pre-existing uncommitted data"), 0644); err != nil {
				t.Fatal(err)
			}
			// Establish both persistent guards before interruption. Neither may be
			// replaced/unlinked by enablement or its retry after process death.
			guards := map[string]os.FileInfo{}
			for _, name := range []string{"worktree.guard", "write.guard"} {
				path := filepath.Join(s.meta, name)
				f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Close(); err != nil {
					t.Fatal(err)
				}
				guards[name], err = os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, "-test.run=^TestFilesystemEnableProcessHelper$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "PEBBLE_ENABLE_ROOT="+s.root, "PEBBLE_ENABLE_POINT="+point)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("actual enablement process interruption: %v\n%s", err, out)
			}
			s, err = Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			marker, err := s.marker()
			if err != nil || marker != formatMarker && marker != filesystemMarker {
				t.Fatalf("torn/unsupported marker after exit: %q %v", marker, err)
			}
			for i := 0; i < 2; i++ {
				if err = s.EnableFilesystem(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for name, before := range guards {
				after, err := os.Lstat(filepath.Join(s.meta, name))
				if err != nil || !os.SameFile(before, after) {
					t.Fatalf("guard identity changed: %s %v", name, err)
				}
			}
			if data, err := os.ReadFile(working); err != nil || string(data) != "pre-existing uncommitted data" {
				t.Fatalf("working data changed: %q %v", data, err)
			}
			var restored bytes.Buffer
			if err = s.CopyFile(ctx, manifest, &restored); err != nil || restored.String() != "preserved historical bytes" {
				t.Fatalf("history changed: %q %v", restored.String(), err)
			}
		})
	}
}
