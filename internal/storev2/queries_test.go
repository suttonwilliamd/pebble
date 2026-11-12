package storev2

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSymbolicHeadCASAndTargetValidation(t *testing.T) {
	ctx := context.Background()
	s, err := InitRepository(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateHead(ctx, "heads/wrong", "heads/main"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale HEAD: %v", err)
	}
	if err = s.UpdateHead(ctx, "heads/main", "heads/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing branch: %v", err)
	}
	byName, _ := corpus(t)
	putGoldens(t, s, byName, "commit-root")
	root := goldenID(t, byName["commit-root"])
	if err = s.UpdateRef(ctx, "heads/topic", nil, root); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateHead(ctx, "heads/main", "heads/topic"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if head, err := reopened.Head(ctx); err != nil || head != "heads/topic" {
		t.Fatalf("HEAD: %s %v", head, err)
	}
	if err = reopened.UpdateHead(ctx, "heads/main", "heads/topic"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale retry accepted: %v", err)
	}
	if err = os.WriteFile(filepath.Join(s.meta, "HEAD"), []byte("ref: heads/topic\ntrailing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Head(ctx); err == nil {
		t.Fatal("trailing HEAD metadata accepted")
	}
}

func TestCatalogRejectsCaseAndAncestorCollisions(t *testing.T) {
	for _, names := range [][]string{{"heads/main", "heads/MAIN"}, {"heads/main", "heads/main-foo", "heads/main/topic"}} {
		s := newStore(t)
		byName, _ := corpus(t)
		putGoldens(t, s, byName, "commit-root")
		id := goldenID(t, byName["commit-root"])
		for _, name := range names {
			// Inject independently valid physical ref records to model catalog corruption.
			data := make([]byte, 4+len(name)+33)
			data[0] = byte(len(name) >> 24)
			data[1] = byte(len(name) >> 16)
			data[2] = byte(len(name) >> 8)
			data[3] = byte(len(name))
			copy(data[4:], name)
			data[4+len(name)] = byte(id.Kind)
			copy(data[5+len(name):], id.Digest[:])
			if err := os.WriteFile(s.refPath(name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.ListRefs(context.Background()); err == nil {
			t.Fatalf("collision accepted: %v", names)
		}
	}
}

func TestHEADPublicationErrorsDoNotAcknowledgeSuccess(t *testing.T) {
	ctx := context.Background()
	byName, _ := corpus(t)
	for _, point := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
		t.Run(point, func(t *testing.T) {
			s, err := InitRepository(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			putGoldens(t, s, byName, "commit-old")
			id := goldenID(t, byName["commit-old"])
			if err = s.UpdateRef(ctx, "heads/main", nil, id); err != nil {
				t.Fatal(err)
			}
			if err = s.UpdateRef(ctx, "heads/topic", nil, id); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("HEAD barrier failure")
			s.fault = func(stage, path string) error {
				if stage == point && filepath.Base(path) == "HEAD" {
					return injected
				}
				return nil
			}
			if err = s.UpdateHead(ctx, "heads/main", "heads/topic"); !errors.Is(err, injected) {
				t.Fatalf("HEAD error hidden: %v", err)
			}
			s, err = Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			head, err := s.Head(ctx)
			if err != nil || head != "heads/main" && head != "heads/topic" {
				t.Fatalf("partial HEAD: %s %v", head, err)
			}
			assertRef(t, s, head, id)
			if err = s.UpdateHead(ctx, head, "heads/topic"); err != nil {
				t.Fatal(err)
			}
			if got, err := s.Head(ctx); err != nil || got != "heads/topic" {
				t.Fatalf("retry HEAD: %s %v", got, err)
			}
			assertRef(t, s, "heads/topic", id)
		})
	}
}
