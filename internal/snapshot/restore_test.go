package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestore(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, "sub/file", "original")
	hash := commitTest(t, r, "first")
	put(t, r, "sub/file", "edits")
	if err := r.Restore("sub/file"); err != nil {
		t.Fatal(err)
	}
	if content(t, r, "sub/file") != "original" {
		t.Fatal("restore failed")
	}
	os.Remove(filepath.Join(r.RootPath(), "sub/file"))
	os.Remove(filepath.Join(r.RootPath(), "sub"))
	if err := r.Restore("sub/file"); err != nil {
		t.Fatal(err)
	}
	if content(t, r, "sub/file") != "original" {
		t.Fatal("deleted restore failed")
	}
	if got, _ := r.GetRef("heads/main"); got != hash {
		t.Fatal("restore changed history")
	}
	put(t, r, "untracked", "precious")
	if err := r.Restore("untracked"); err == nil {
		t.Fatal("untracked restore allowed")
	}
	if content(t, r, "untracked") != "precious" {
		t.Fatal("untracked file changed")
	}
	if err := r.Restore("../outside"); err == nil {
		t.Fatal("escape allowed")
	}
	os.Remove(filepath.Join(r.RootPath(), "sub/file"))
	os.Mkdir(filepath.Join(r.RootPath(), "sub/file"), 0755)
	if err := r.Restore("sub/file"); err == nil {
		t.Fatal("directory overwritten")
	}
}
func TestRestorePreflight(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, "file", "good")
	commitTest(t, r, "first")
	entries, _ := r.TreeEntries()
	hash := entries[0].Hash
	os.WriteFile(filepath.Join(r.ObjectsPath(), hash[:2], hash[2:]), []byte("broken"), 0644)
	put(t, r, "file", "keep edits")
	if err := r.Restore("file"); err == nil {
		t.Fatal("corrupt restore allowed")
	}
	if content(t, r, "file") != "keep edits" {
		t.Fatal("restore lost edits before validation")
	}
}
