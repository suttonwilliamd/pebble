package storev2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/processlock"
)

func oldMarkerStore(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	if err := os.WriteFile(filepath.Join(s.meta, "format"), []byte(formatMarker), 0600); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestFilesystemCapabilityWriterGate(t *testing.T) {
	ctx := context.Background()
	s := oldMarkerStore(t)
	// Ordinary old-marker histories remain usable without implicit enablement.
	manifest, err := s.IngestFile(ctx, bytes.NewReader([]byte("literal target")), nil)
	if err != nil {
		t.Fatal(err)
	}
	regular, err := s.CreateTree(ctx, []TreeEntry{{Path: "kept", Executable: true, Manifest: manifest}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []byte{2, 3} {
		payload := appendText([]byte{0, 0, 0, 1}, "link")
		payload = append(payload, mode)
		payload = appendLink(payload, manifest)
		id, err := NewID(Tree, payload)
		if err != nil {
			t.Fatal("pure ID construction rejected extension", err)
		}
		header, err := Header(Tree, uint64(len(payload)))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Put(ctx, id, bytes.NewReader(append(header[:], payload...))); err == nil {
			t.Fatal("direct Put bypassed explicit capability enablement")
		}
		if _, err = s.CreateTree(ctx, []TreeEntry{{Path: "link", Symlink: true, DirectoryLink: mode == 3, Manifest: manifest}}); err == nil {
			t.Fatal("CreateTree bypassed explicit capability enablement")
		}
		if _, err = os.Lstat(s.objectPath(id)); !os.IsNotExist(err) {
			t.Fatalf("unsupported object published: %v", err)
		}
	}
	if marker, err := s.marker(); err != nil || marker != formatMarker {
		t.Fatalf("implicit marker upgrade: %q %v", marker, err)
	}
	if err = s.EnableFilesystem(ctx); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []bool{false, true} {
		id, err := s.CreateTree(ctx, []TreeEntry{{Path: "link", Symlink: true, DirectoryLink: directory, Manifest: manifest}})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Verify(ctx, id); err != nil {
			t.Fatal(err)
		}
		entries, err := s.ReadTree(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || !entries[0].Symlink || entries[0].DirectoryLink != directory {
			t.Fatalf("link kind lost: %+v", entries)
		}
	}
	// Every pre-enable object remains exactly addressable and verified.
	for _, id := range before {
		if err = s.Verify(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.ReadTree(ctx, regular)
	if err != nil || len(entries) != 1 || !entries[0].Executable {
		t.Fatalf("old executable history lost: %+v %v", entries, err)
	}
}
func TestFilesystemCapabilityRefGraphGate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	manifest, err := s.IngestFile(ctx, bytes.NewReader([]byte("target")), nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := s.CreateTree(ctx, []TreeEntry{{Path: "link", Symlink: true, Manifest: manifest}})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := s.CreateCommit(ctx, CommitData{Tree: tree, Author: "Fixture", Email: "fixture@example.invalid", Offset: "+0000"})
	if err != nil {
		t.Fatal(err)
	}
	// A forged old-marker root containing extension objects must not publish refs.
	if err = os.WriteFile(filepath.Join(s.meta, "format"), []byte(formatMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateRef(ctx, "heads/forged", nil, commit); err == nil {
		t.Fatal("ref graph bypassed capability gate")
	}
	if _, err = os.Lstat(s.refPath("heads/forged")); !os.IsNotExist(err) {
		t.Fatalf("unsupported ref published: %v", err)
	}
}
func TestFilesystemEnablementEveryPublicationBoundary(t *testing.T) {
	for _, point := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
		t.Run(point, func(t *testing.T) {
			s := oldMarkerStore(t)
			ctx := context.Background()
			manifest, err := s.IngestFile(ctx, bytes.NewReader([]byte("preserved bytes")), nil)
			if err != nil {
				t.Fatal(err)
			}
			stopped := errors.New("interrupted enablement")
			s.fault = func(stage, path string) error {
				if stage == point && path == filepath.Join(s.meta, "format") {
					return stopped
				}
				return nil
			}
			if err = s.EnableFilesystem(ctx); !errors.Is(err, stopped) {
				t.Fatalf("boundary not observed: %v", err)
			}
			s, err = Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			marker, err := s.marker()
			if err != nil || marker != formatMarker && marker != filesystemMarker {
				t.Fatalf("torn capability state: %q %v", marker, err)
			}
			for i := 0; i < 2; i++ {
				if err = s.EnableFilesystem(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			if err = s.CopyFile(ctx, manifest, &out); err != nil || out.String() != "preserved bytes" {
				t.Fatalf("history changed: %q %v", out.String(), err)
			}
		})
	}
}
func TestFilesystemEnablementRespectsExistingGuardsAndRecovery(t *testing.T) {
	for _, name := range []string{"worktree.guard", "write.guard", "worktree-journal", "worktree.lock"} {
		t.Run(name, func(t *testing.T) {
			s := oldMarkerStore(t)
			var unlock func() error
			var err error
			p := filepath.Join(s.meta, name)
			if name == "worktree-journal" {
				err = os.Mkdir(p, 0700)
			} else if name == "worktree.lock" {
				err = os.WriteFile(p, []byte("preserved legacy owner"), 0600)
			} else {
				unlock, err = processlock.Acquire(p)
			}
			if err != nil {
				t.Fatal(err)
			}
			if unlock != nil {
				defer unlock()
			}
			if err = s.EnableFilesystem(context.Background()); err == nil {
				t.Fatal("active owner/pending recovery bypassed")
			}
			marker, err := s.marker()
			if err != nil || marker != formatMarker {
				t.Fatalf("marker changed despite exclusion: %q %v", marker, err)
			}
		})
	}
}
