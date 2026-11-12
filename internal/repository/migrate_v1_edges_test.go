package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestMigrationOverDepthRejectsEntireHistory(t *testing.T) {
	source, destination, h := frozenMigration(t)
	r, err := snapshot.NewRepository(source)
	if err != nil {
		t.Fatal(err)
	}
	parent := h.Refs["heads/main"]
	c := h.Commits[parent]
	c.Hash = ""
	c.Message = "complete bounded history, never a truncated import"
	for i := 0; i < storev2.MaxGraphDepth; i++ {
		c.Parent = parent
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		object := snapshot.NewObject(snapshot.ObjectTypeCommit, data)
		if err := r.StoreObject(object); err != nil {
			t.Fatal(err)
		}
		parent = object.Hash
	}
	if err := r.SetRef("heads/main", parent); err != nil {
		t.Fatal(err)
	}
	before := sourceState(t, source)
	result, err := MigrateV1(context.Background(), source, destination, MigrationOptions{})
	if err == nil || result != nil || !strings.Contains(err.Error(), "graph resource limit") {
		t.Fatalf("over-depth history was not explicitly rejected: %+v %v", result, err)
	}
	unchangedSource(t, source, before)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("over-depth history published a shortened destination")
	}
	if stages, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".pebble-migrate-*")); err != nil || len(stages) != 0 {
		t.Fatalf("over-depth error leaked stage: %v %v", stages, err)
	}
}

func TestMigrationExecutableMetadataAndHead(t *testing.T) {
	source, destination, h := frozenMigration(t)
	original := h.Refs["heads/main"]
	c := h.Commits[original]
	entries := append([]snapshot.TreeEntry{}, h.Trees[c.Tree]...)
	entries[0].Mode = "-rwxr-xr-x"
	tree := snapshot.CreateTree(entries)
	r, err := snapshot.NewRepository(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.StoreObject(tree); err != nil {
		t.Fatal(err)
	}
	c.Tree, c.Parent, c.Message = tree.Hash, original, "preserve executable metadata"
	executable := storeMigrationCommit(t, source, c)
	if err := r.SetRef("heads/main", original); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRef("heads/executable", executable); err != nil {
		t.Fatal(err)
	}
	before := sourceState(t, source)
	result, err := MigrateV1(context.Background(), source, destination, MigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := storev2.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	id := mappedID(t, result, tree.Hash)
	got, err := s.ReadTree(context.Background(), id)
	if err != nil || len(got) != 1 || !got[0].Executable || got[0].Path != entries[0].Name {
		t.Fatalf("historical executable bit lost: %+v %v", got, err)
	}
	unchangedSource(t, source, before)
	executableDestination := filepath.Join(filepath.Dir(destination), "executable-head")
	if runtime.GOOS == "windows" {
		// Windows ordinary files cannot expose Unix execute permission bits.
		// Do not claim HEAD restoration succeeded by silently clearing metadata.
		assertMigrationRejected(t, source, executableDestination, MigrationOptions{Head: "executable"}, nil)
	} else {
		result, err := MigrateV1(context.Background(), source, executableDestination, MigrationOptions{Head: "executable"})
		if err != nil || result.Head != "heads/executable" {
			t.Fatalf("executable HEAD: %+v %v", result, err)
		}
		info, err := os.Stat(filepath.Join(executableDestination, entries[0].Name))
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("executable HEAD mode differs: %v %v", info, err)
		}
		unchangedSource(t, source, before)
	}
}

func TestMigrationUnicodeNormalizationNeverRenames(t *testing.T) {
	source, destination, h := frozenMigration(t)
	base := filepath.Dir(source)
	probe := filepath.Join(base, "namespace-probe")
	if err := os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	composed, decomposed := "caf\u00e9.bin", "cafe\u0301.bin"
	if err := os.WriteFile(filepath.Join(probe, composed), []byte("probe"), 0600); err != nil {
		t.Fatal(err)
	}
	_, aliasErr := os.Lstat(filepath.Join(probe, decomposed))
	aliases := aliasErr == nil
	if aliasErr != nil && !os.IsNotExist(aliasErr) {
		t.Fatal(aliasErr)
	}
	if err := os.RemoveAll(probe); err != nil {
		t.Fatal(err)
	}
	r, err := snapshot.NewRepository(source)
	if err != nil {
		t.Fatal(err)
	}
	data := map[string][]byte{composed: []byte{0, 255}, decomposed: []byte{128, 10, 13}}
	entries := []snapshot.TreeEntry{}
	for _, name := range []string{composed, decomposed} {
		blob := snapshot.NewObject(snapshot.ObjectTypeBlob, data[name])
		if err := r.StoreObject(blob); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, snapshot.TreeEntry{Name: name, Mode: "-rw-r--r--", Type: snapshot.ObjectTypeBlob, Hash: blob.Hash})
	}
	tree := snapshot.CreateTree(entries)
	if err := r.StoreObject(tree); err != nil {
		t.Fatal(err)
	}
	c := h.Commits[h.Refs["heads/main"]]
	c.Tree = tree.Hash
	storeMigrationCommit(t, source, c)
	if aliases {
		t.Log("filesystem aliases normalization spellings; migration must refuse")
		assertMigrationRejected(t, source, destination, MigrationOptions{}, nil)
		return
	}
	before := sourceState(t, source)
	result, err := MigrateV1(context.Background(), source, destination, MigrationOptions{})
	if err != nil || result == nil {
		t.Fatalf("distinct normalization spellings rejected: %v", err)
	}
	for name, want := range data {
		got, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("normalized/renamed path %q: %q %v", name, got, err)
		}
	}
	unchangedSource(t, source, before)
}
