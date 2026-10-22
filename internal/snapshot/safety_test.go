package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedCommitDoesNotPublish(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, "a", "first")
	before := commitTest(t, r, "first")
	put(t, r, "a", "changed")
	obj := NewObject(ObjectTypeBlob, []byte("changed"))
	p := filepath.Join(r.ObjectsPath(), obj.Hash[:2])
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	// Make the target object a directory, forcing a storage error on every OS.
	if err := os.Mkdir(filepath.Join(p, obj.Hash[2:]), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit("must fail", "t", "e"); err == nil {
		t.Fatal("storage failure ignored")
	}
	if after, _ := r.GetRef("heads/main"); after != before {
		t.Fatal("failed commit published")
	}
	if content(t, r, "a") != "changed" {
		t.Fatal("failed commit changed working file")
	}
}
func TestTreeValidation(t *testing.T) {
	blob := NewObject(ObjectTypeBlob, []byte("content"))
	entry := func(name string) TreeEntry { return TreeEntry{Name: name, Hash: blob.Hash, Type: ObjectTypeBlob} }
	for _, entries := range [][]TreeEntry{{entry("x"), entry("X")}, {entry("a"), entry("a/b")}, {entry("../outside")}} {
		if err := validateEntries(entries); err == nil {
			t.Fatalf("invalid tree accepted: %v", entries)
		}
	}
	r := newTestRepo(t)
	put(t, r, "good", "safe")
	commitTest(t, r, "safe")
	if err := r.StoreObject(blob); err != nil {
		t.Fatal(err)
	}
	tree := CreateTree([]TreeEntry{entry("../outside")})
	if err := r.StoreObject(tree); err != nil {
		t.Fatal(err)
	}
	c := CreateCommit(tree.Hash, "", "t", "e", "malicious tree")
	if err := r.StoreObject(c); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRef("heads/bad", c.Hash); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("bad"); err == nil {
		t.Fatal("unsafe checkout allowed")
	}
	if content(t, r, "good") != "safe" {
		t.Fatal("unsafe checkout changed files")
	}
	if _, err := r.Verify(); err == nil {
		t.Fatal("fsck accepted unsafe tree")
	}
}
func TestIgnoredCheckoutCollision(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, ".pebbleignore", "*.log\n")
	commitTest(t, r, "base")
	if err := r.CreateBranch("base"); err != nil {
		t.Fatal(err)
	}
	// Add a previously committed file using an empty ignore file, then restore the rule.
	put(t, r, ".pebbleignore", "")
	put(t, r, "error.log", "tracked target")
	commitTest(t, r, "track log")
	put(t, r, ".pebbleignore", "*.log\n")
	commitTest(t, r, "ignore logs")
	if err := r.Checkout("base"); err != nil {
		t.Fatal(err)
	}
	put(t, r, "error.log", "precious ignored file")
	if err := r.Checkout("main"); err == nil {
		t.Fatal("ignored collision overwritten")
	}
	if content(t, r, "error.log") != "precious ignored file" {
		t.Fatal("lost ignored file")
	}
}
func TestUnbornBranches(t *testing.T) {
	r := newTestRepo(t)
	if err := r.CreateBranch("empty"); err != nil {
		t.Fatal(err)
	}
	put(t, r, "file", "committed")
	commitTest(t, r, "main")
	if err := r.Checkout("empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.RootPath(), "file")); !os.IsNotExist(err) {
		t.Fatal("empty branch not restored")
	}
	if n, err := r.Verify(); err != nil || n != 3 {
		t.Fatalf("verify unborn: %d %v", n, err)
	}
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if content(t, r, "file") != "committed" {
		t.Fatal("main not restored")
	}
}
