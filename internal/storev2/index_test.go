package storev2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIndexPublicationEveryBoundary(t *testing.T) {
	for _, point := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			s, err := InitRepository(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableStaging(ctx); err != nil {
				t.Fatal(err)
			}
			treeFor := func(data string) ID {
				t.Helper()
				manifest, err := s.IngestFile(ctx, bytes.NewBufferString(data), nil)
				if err != nil {
					t.Fatal(err)
				}
				tree, err := s.CreateTree(ctx, []TreeEntry{{Path: "asset", Manifest: manifest}})
				if err != nil {
					t.Fatal(err)
				}
				return tree
			}
			a := &IndexState{Head: "heads/main", Tree: treeFor("immutable A\x00\xff\r\n")}
			b := &IndexState{Head: "heads/main", Tree: treeFor("immutable B\xff\x00")}
			if err = s.WriteIndex(ctx, a); err != nil {
				t.Fatal(err)
			}
			interrupted := errors.New("index publication interrupted")
			s.fault = func(stage, path string) error {
				if stage == point && path == filepath.Join(s.meta, "index") {
					return interrupted
				}
				return nil
			}
			if err = s.WriteIndex(ctx, b); !errors.Is(err, interrupted) {
				t.Fatalf("boundary not reached: %v", err)
			}
			s, err = Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadIndex(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, a) && !reflect.DeepEqual(got, b) {
				t.Fatalf("torn or fabricated index: %#v", got)
			}
			entries, err := s.ReadTree(ctx, got.Tree)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err = s.CopyFile(ctx, entries[0].Manifest, &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != "immutable A\x00\xff\r\n" && out.String() != "immutable B\xff\x00" {
				t.Fatalf("acknowledged index bytes unavailable: %q", out.String())
			}
			if err = s.WriteIndex(ctx, b); err != nil {
				t.Fatal(err)
			}
			got, err = s.ReadIndex(ctx)
			if err != nil || !reflect.DeepEqual(got, b) {
				t.Fatalf("retry failed: %#v %v", got, err)
			}
		})
	}
}
func TestCommitterExtensionIdentityAndCapability(t *testing.T) {
	ctx := context.Background()
	s, err := InitRepository(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := s.CreateTree(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := CommitData{Tree: tree, Parents: []ID{}, Author: "Historical Author", Email: "historical@example.invalid", Seconds: -42, Nanoseconds: 123, Offset: "-0000", Message: "preserve legacy"}
	old, err := s.CreateCommit(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	next := legacy
	next.Committer = "Actual Committer"
	next.CommitterEmail = "committer@example.invalid"
	if _, err = s.CreateCommit(ctx, next); err == nil {
		t.Fatal("committer extension bypassed capability gate")
	}
	if err = s.EnableStaging(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateCommit(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadCommit(ctx, id)
	if err != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("committer provenance lost: %#v %v", got, err)
	}
	historical, err := s.ReadCommit(ctx, old)
	if err != nil || !reflect.DeepEqual(historical, legacy) {
		t.Fatalf("invented historical committer: %#v %v", historical, err)
	}
	legacyAgain, err := s.CreateCommit(ctx, historical)
	if err != nil || legacyAgain != old {
		t.Fatal("legacy commit bytes/ID changed", err)
	}
	next.CommitterEmail = ""
	if _, err = s.CreateCommit(ctx, next); err == nil {
		t.Fatal("partial committer accepted")
	}
	if err = s.WriteIndex(ctx, &IndexState{Head: "heads/main", Tree: tree, Next: id}); err != nil {
		t.Fatal(err)
	}
	// A forged downgraded marker cannot publish/read an extension graph as old.
	if err = os.WriteFile(filepath.Join(s.meta, "format"), []byte(filesystemMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateRef(ctx, "heads/forged", nil, id); err == nil {
		t.Fatal("extension graph bypassed marker validation")
	}
	if _, err = s.ReadIndex(ctx); err == nil {
		t.Fatal("unexpected staging facts accepted after downgrade")
	}
}
