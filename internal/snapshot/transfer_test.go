package snapshot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func copyBundle(t *testing.T, b *Bundle) *Bundle {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var out Bundle
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}
func TestTransferCompleteHistory(t *testing.T) {
	source := newTestRepo(t)
	put(t, source, "text", "first")
	first := commitTest(t, source, "first")
	put(t, source, "asset.bin", string([]byte{0, 255, 12, 0, 3}))
	put(t, source, "text", "second")
	second := commitTest(t, source, "second")
	bundle, err := source.Export("heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Hash != second || len(bundle.Objects) != 7 {
		t.Fatalf("incomplete bundle: %d objects", len(bundle.Objects))
	}
	dest := newTestRepo(t)
	if err = dest.ApplyBundle(bundle, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = dest.Verify(); err != nil {
		t.Fatal(err)
	}
	c, err := dest.GetCommit(second)
	if err != nil || c.Parent != first {
		t.Fatalf("lost history %v %v", c, err)
	}
	if err = dest.ApplyBundle(bundle, ""); !errors.Is(err, ErrRefConflict) {
		t.Fatalf("stale CAS accepted: %v", err)
	}
	if err = dest.ApplyBundle(bundle, second); err != nil {
		t.Fatal("idempotent retry:", err)
	}
	put(t, source, "text", "third")
	third := commitTest(t, source, "third")
	next, _ := source.Export("heads/main")
	if err = dest.ApplyBundle(next, second); err != nil {
		t.Fatal(err)
	}
	if err = dest.ApplyBundle(bundle, third); !errors.Is(err, ErrRefConflict) {
		t.Fatalf("rewind accepted: %v", err)
	}
}
func TestInvalidBundlesNeverPublish(t *testing.T) {
	source := newTestRepo(t)
	put(t, source, "a", "safe")
	commitTest(t, source, "initial")
	base, _ := source.Export("heads/main")
	cases := map[string]func(*Bundle){
		"version":            func(b *Bundle) { b.Version = 99 },
		"unsafe ref":         func(b *Bundle) { b.Ref = "heads/../../escape" },
		"invalid hash":       func(b *Bundle) { b.Hash = "a" },
		"missing dependency": func(b *Bundle) { b.Objects = b.Objects[:len(b.Objects)-1] },
		"tampered content":   func(b *Bundle) { b.Objects[0].Content = []byte("bad") },
		"duplicate":          func(b *Bundle) { b.Objects = append(b.Objects, b.Objects[0]) },
		"unreachable":        func(b *Bundle) { b.Objects = append(b.Objects, NewObject(ObjectTypeBlob, []byte("junk"))) },
		"wrong type": func(b *Bundle) {
			for _, o := range b.Objects {
				if o.Type == ObjectTypeCommit {
					o.Type = ObjectTypeBlob
				}
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := newTestRepo(t)
			b := copyBundle(t, base)
			mutate(b)
			if err := r.ApplyBundle(b, ""); err == nil {
				t.Fatal("invalid bundle accepted")
			}
			if hash, _ := r.GetRef("heads/main"); hash != "" {
				t.Fatal("invalid bundle published")
			}
			if n, err := r.Verify(); err != nil || n != 0 {
				t.Fatalf("repository changed: %d %v", n, err)
			}
		})
	}
	if err := newTestRepo(t).ApplyBundle(nil, ""); err == nil {
		t.Fatal("nil bundle accepted")
	}
}
func TestImportProtectsCheckedOutBranch(t *testing.T) {
	source := newTestRepo(t)
	commitTest(t, source, "initial")
	b, _ := source.Export("heads/main")
	dest := newTestRepo(t)
	if err := dest.ImportBundle(b, ""); err == nil {
		t.Fatal("import changed checked-out branch")
	}
	if hash, _ := dest.GetRef("heads/main"); hash != "" {
		t.Fatal("checked-out ref changed")
	}
	b.Ref = "heads/incoming/main"
	if err := dest.ImportBundle(b, ""); err != nil {
		t.Fatal(err)
	}
}

func TestObjectTypeCollisionPreservesExistingObject(t *testing.T) {
	r := newTestRepo(t)
	tree := NewObject(ObjectTypeTree, []byte("[]"))
	blob := NewObject(ObjectTypeBlob, []byte("[]"))
	if err := r.StoreObject(tree); err != nil {
		t.Fatal(err)
	}
	if err := r.StoreObject(blob); err == nil {
		t.Fatal("same content with a conflicting type accepted")
	}
	got, err := r.GetObject(tree.Hash)
	if err != nil || got.Type != ObjectTypeTree {
		t.Fatal("existing object damaged")
	}
}

func TestRefsIgnoreAtomicTemporaryFiles(t *testing.T) {
	r := newTestRepo(t)
	if err := os.WriteFile(filepath.Join(r.refsPath, "heads", ".pebble-tmp-test"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	refs, err := r.GetRefs()
	if err != nil || len(refs) != 1 {
		t.Fatalf("temporary ref leaked: %v %v", refs, err)
	}
}

func TestTransferBranchCollision(t *testing.T) {
	source := newTestRepo(t)
	commitTest(t, source, "empty")
	b, _ := source.Export("heads/main")
	dest := newTestRepo(t)
	b.Ref = "heads/Main"
	if err := dest.ApplyBundle(b, ""); err == nil {
		t.Fatal("case collision accepted")
	}
	b.Ref = "heads/main/nested"
	if err := dest.ApplyBundle(b, ""); err == nil {
		t.Fatal("directory collision accepted")
	}
}
