package storev2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemHeadPinsRejectChangedBranches(t *testing.T) {
	for _, changed := range []string{"source", "target"} {
		t.Run(changed, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			tree, err := s.CreateTree(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.CreateCommit(ctx, CommitData{Tree: tree, Author: "Fixture", Email: "fixture@example.invalid", Offset: "+0000", Message: "original"})
			if err != nil {
				t.Fatal(err)
			}
			next, err := s.CreateCommit(ctx, CommitData{Tree: tree, Parents: []ID{original}, Author: "Fixture", Email: "fixture@example.invalid", Offset: "+0000", Message: "external advance"})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"source", "target"} {
				if err = s.UpdateRef(ctx, "heads/"+name, nil, original); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.UpdateHead(ctx, "", "heads/source"); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(s.meta, "HEAD"))
			if err != nil {
				t.Fatal(err)
			}
			if err = s.UpdateRef(ctx, "heads/"+changed, &original, next); err != nil {
				t.Fatal(err)
			}
			err = s.UpdateHead(ctx, "heads/source", "heads/target", HeadPin{Name: "heads/source", ID: original}, HeadPin{Name: "heads/target", ID: original})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("changed branch pin not rejected: %v", err)
			}
			after, err := os.ReadFile(filepath.Join(s.meta, "HEAD"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("stale pins changed HEAD: %q %v", after, err)
			}
			sourceID, targetID := original, original
			if changed == "source" {
				sourceID = next
			} else {
				targetID = next
			}
			if err = s.UpdateHead(ctx, "heads/source", "heads/target", HeadPin{Name: "heads/source", ID: sourceID}, HeadPin{Name: "heads/target", ID: targetID}); err != nil {
				t.Fatal(err)
			}
			if head, err := s.Head(ctx); err != nil || head != "heads/target" {
				t.Fatalf("fresh pinned CAS lost: %s %v", head, err)
			}
		})
	}
}
