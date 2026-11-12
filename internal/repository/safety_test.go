package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestUnsafeTreeCannotChangeWorkingFilesOrHead(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "safe", []byte("preserve"))
	parent := committed(t, r, "base")
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{"CON"}, {"Dir/a", "dir/b"}, {"A/b", "a"}} {
		treeEntries := []storev2.TreeEntry{}
		for _, path := range paths {
			treeEntries = append(treeEntries, storev2.TreeEntry{Path: path, Manifest: entries[0].Manifest})
		}
		tree, err := r.store.CreateTree(context.Background(), treeEntries)
		if err != nil {
			t.Fatal(err)
		}
		bad, err := r.store.CreateCommit(context.Background(), storev2.CommitData{Tree: tree, Parents: []storev2.ID{parent}, Author: "A", Email: "a@b", Offset: "+0000", Message: "unsafe"})
		if err != nil {
			t.Fatal(err)
		}
		name := "bad"
		if paths[0] != "CON" {
			name = "bad-" + paths[0][:1]
		}
		if err = r.store.UpdateRef(context.Background(), "heads/"+name, nil, bad); err != nil {
			t.Fatal(err)
		}
		if err = r.Checkout(name); err == nil {
			t.Fatalf("unsafe tree accepted: %v", paths)
		}
		head, err := r.GetHead()
		if err != nil || head != "heads/main" {
			t.Fatalf("HEAD changed: %s %v", head, err)
		}
		got, err := os.ReadFile(filepath.Join(r.root, "safe"))
		if err != nil || string(got) != "preserve" {
			t.Fatalf("working file changed: %q %v", got, err)
		}
	}
}

func TestSymlinkAndSpecialPathGuards(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "tracked", []byte("original"))
	committed(t, r, "base")
	outside := filepath.Join(t.TempDir(), "precious")
	if err = os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(r.root, "tracked")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(r.root, "tracked")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err = r.Status(); err == nil {
		t.Fatal("symlink status accepted")
	}
	if _, err = r.Commit("bad", "A", "a@b"); err == nil {
		t.Fatal("symlink commit accepted")
	}
	if err = r.Restore("tracked"); err == nil {
		t.Fatal("symlink restore accepted")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "outside" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}
