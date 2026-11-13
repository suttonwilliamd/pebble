package repository

import (
	"context"
	"fmt"
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
	for index, paths := range [][]string{{"CON"}, {"COM¹"}, {"LPT².txt"}, {"CONIN$"}, {"CONOUT$"}, {"AUX .txt"}, {"Dir/a", "dir/b"}, {"A/b", "a"}} {
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
		name := fmt.Sprintf("bad-%d", index)
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
	changes, err := r.Status()
	if err != nil || len(changes) != 1 || changes[0] != (Change{" M", "tracked"}) {
		t.Fatalf("link type change not detected: %v %v", changes, err)
	}
	if err = r.Restore("tracked"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(r.root, "tracked"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("restore did not restore tracked leaf type: %v", err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "outside" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}
