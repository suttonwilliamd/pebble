package repository

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

const composedName = "\u00e9"
const decomposedName = "e\u0301"

// Ask the actual fixture filesystem, not the OS name or a Unicode heuristic.
func normalizationAliases(t *testing.T, root string) bool {
	t.Helper()
	probe, err := os.MkdirTemp(filepath.Join(root, ".pebble"), "normalization-probe-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(probe)
	if err = os.WriteFile(filepath.Join(probe, composedName), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(probe, decomposedName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		return false
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	return true
}

func normalizationFixture(t *testing.T) (*Repository, storev2.ID) {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "precious", []byte("preserve original tracked bytes"))
	return r, committed(t, r, "base")
}
func normalizationBranch(t *testing.T, r *Repository, parent storev2.ID, files []struct {
	path string
	data []byte
}) storev2.ID {
	t.Helper()
	ctx := context.Background()
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		manifest, err := r.store.IngestFile(ctx, bytes.NewReader(file.data), nil)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, storev2.TreeEntry{Path: file.path, Manifest: manifest})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	tree, err := r.store.CreateTree(ctx, entries)
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.store.CreateCommit(ctx, storev2.CommitData{Tree: tree, Parents: []storev2.ID{parent}, Author: "Fixture", Email: "fixture@example.com", Offset: "+0000", Message: "normalization paths"})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.UpdateRef(ctx, "heads/normalization", nil, id); err != nil {
		t.Fatal(err)
	}
	return id
}
func assertNormalizationPreserved(t *testing.T, r *Repository, parent storev2.ID) {
	t.Helper()
	head, err := r.GetHead()
	if err != nil || head != "heads/main" {
		t.Fatalf("HEAD changed: %s %v", head, err)
	}
	id, err := r.CurrentID()
	if err != nil || id != parent {
		t.Fatalf("HEAD target changed: %s %v", id, err)
	}
	got, err := os.ReadFile(filepath.Join(r.root, "precious"))
	if err != nil || string(got) != "preserve original tracked bytes" {
		t.Fatalf("tracked bytes changed: %q %v", got, err)
	}
}

func TestCheckoutRefusesNormalizationAliasingTrees(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "files"
		if directory {
			name = "directory-components"
		}
		t.Run(name, func(t *testing.T) {
			r, parent := normalizationFixture(t)
			if !normalizationAliases(t, r.root) {
				t.Skip("fixture filesystem distinguishes composed and decomposed spellings")
			}
			a, b := composedName+".txt", decomposedName+".txt"
			if directory {
				a, b = composedName+"/one", decomposedName+"/two"
			}
			normalizationBranch(t, r, parent, []struct {
				path string
				data []byte
			}{{a, []byte("first revision")}, {b, []byte("second revision")}})
			if err := r.Checkout("normalization"); err == nil {
				t.Fatal("checkout acknowledged an unrepresentable tree")
			}
			assertNormalizationPreserved(t, r, parent)
			for _, path := range []string{a, b} {
				if _, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(path))); !os.IsNotExist(err) {
					t.Fatalf("target path mutated: %s %v", path, err)
				}
			}
		})
	}
}

func TestCheckoutRefusesExistingNormalizationAliases(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "untracked-file"
		if directory {
			name = "existing-directory-component"
		}
		t.Run(name, func(t *testing.T) {
			r, parent := normalizationFixture(t)
			if !normalizationAliases(t, r.root) {
				t.Skip("fixture filesystem distinguishes composed and decomposed spellings")
			}
			existing := composedName + ".txt"
			if directory {
				existing = composedName
			}
			data := []byte("preserve untracked bytes")
			path := existing
			if directory {
				path += "/keep"
			}
			write(t, r.root, path, data)
			// HFS+ can enumerate a different spelling than the one requested at creation.
			children, err := os.ReadDir(r.root)
			if err != nil {
				t.Fatal(err)
			}
			actual := ""
			for _, child := range children {
				if child.Name() == composedName || child.Name() == decomposedName || child.Name() == composedName+".txt" || child.Name() == decomposedName+".txt" {
					actual = child.Name()
				}
			}
			if actual == "" {
				t.Fatal("fixture filename not enumerated")
			}
			target := composedName
			if actual == composedName || actual == composedName+".txt" {
				target = decomposedName
			}
			if directory {
				target += "/incoming"
			} else {
				target += ".txt"
			}
			// Identical untracked bytes must not cause differently spelled aliases to be
			// adopted and HEAD published; directory aliases must not merge distinct trees.
			normalizationBranch(t, r, parent, []struct {
				path string
				data []byte
			}{{target, data}})
			if _, err = r.safePath(target); err == nil {
				t.Fatal("working path resolved through a differently spelled filesystem alias")
			}
			if err = r.Checkout("normalization"); err == nil {
				t.Fatal("checkout acknowledged an existing normalization alias")
			}
			assertNormalizationPreserved(t, r, parent)
			got, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(path)))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("untracked bytes changed: %q %v", got, err)
			}
			if directory {
				if _, err = os.Lstat(filepath.Join(r.root, filepath.FromSlash(target))); !os.IsNotExist(err) {
					t.Fatalf("alias directory mutated: %v", err)
				}
			}
		})
	}
}

func TestCheckoutPreservesDistinctNormalizationPaths(t *testing.T) {
	r, parent := normalizationFixture(t)
	if normalizationAliases(t, r.root) {
		t.Skip("fixture filesystem aliases composed and decomposed spellings")
	}
	files := []struct {
		path string
		data []byte
	}{{composedName + ".txt", []byte("composed")}, {decomposedName + ".txt", []byte("decomposed")}, {composedName + "/one", []byte("first directory")}, {decomposedName + "/two", []byte("second directory")}}
	target := normalizationBranch(t, r, parent, files)
	if err := r.Checkout("normalization"); err != nil {
		t.Fatal(err)
	}
	id, err := r.CurrentID()
	if err != nil || id != target {
		t.Fatalf("wrong checkout target: %s %v", id, err)
	}
	for _, file := range files {
		got, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(file.path)))
		if err != nil || !bytes.Equal(got, file.data) {
			t.Fatalf("distinct path changed: %s %q %v", file.path, got, err)
		}
	}
	changes, err := r.Status()
	if err != nil || len(changes) != 0 {
		t.Fatalf("checkout tree differs: %v %v", changes, err)
	}
}
