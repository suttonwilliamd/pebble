package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func write(t *testing.T, root, name string, b []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
}
func committed(t *testing.T, r *Repository, message string) storev2.ID {
	t.Helper()
	id, err := r.Commit(message, "Fixture", "fixture@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLegacyAndPartialDiscoveryLeaveDataIntact(t *testing.T) {
	for _, marked := range []bool{false, true} {
		root := t.TempDir()
		legacy, err := snapshot.Init(root)
		if err != nil {
			t.Fatal(err)
		}
		write(t, root, "precious", []byte("legacy bytes"))
		if _, err = legacy.Commit("old", "A", "a@b"); err != nil {
			t.Fatal(err)
		}
		headBefore, err := os.ReadFile(filepath.Join(root, ".pebble", "HEAD"))
		if err != nil {
			t.Fatal(err)
		}
		if marked {
			write(t, root, ".pebble/format", []byte("unknown format\n"))
		}
		sub := filepath.Join(root, "sub")
		if err = os.Mkdir(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err = Open(sub); err == nil {
			t.Fatal("legacy/unknown repository opened")
		}
		if _, err = Init(root); err == nil {
			t.Fatal("legacy repository reinitialized")
		}
		headAfter, _ := os.ReadFile(filepath.Join(root, ".pebble", "HEAD"))
		data, _ := os.ReadFile(filepath.Join(root, "precious"))
		if !bytes.Equal(headBefore, headAfter) || string(data) != "legacy bytes" {
			t.Fatal("source data changed")
		}
	}
	parent, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(parent.root, "nested")
	if err = os.MkdirAll(filepath.Join(nested, ".pebble"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(nested); err == nil {
		t.Fatal("discovery escaped incomplete nested metadata")
	}
	if _, err = snapshot.NewRepository(parent.root); err == nil {
		t.Fatal("legacy engine opened format2")
	}
}

func TestTrackedIgnoreDeletionAndUntrackedAdoption(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "file.log", []byte("one"))
	committed(t, r, "first")
	if err = r.CreateBranch("old"); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, ".pebbleignore", []byte("*.log\n"))
	write(t, r.root, "file.log", []byte("two"))
	committed(t, r, "ignored but tracked")
	if err = os.Remove(filepath.Join(r.root, "file.log")); err != nil {
		t.Fatal(err)
	}
	changes, err := r.Status()
	if err != nil || len(changes) != 1 || changes[0] != (Change{"D", "file.log"}) {
		t.Fatalf("deletion: %v %v", changes, err)
	}
	if err = r.Checkout("old"); err == nil {
		t.Fatal("dirty deletion ignored")
	}
	if err = r.Restore("file.log"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(r.root, "file.log")); err != nil {
		t.Fatal(err)
	}
	committed(t, r, "deletion")
	write(t, r.root, "file.log", []byte("one"))
	if err = r.Checkout("old"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(r.root, "file.log"))
	if string(got) != "one" {
		t.Fatal("identical untracked adoption changed data")
	}
	if err = r.CreateBranch("OLD"); err == nil {
		t.Fatal("case colliding branch accepted")
	}
}

func TestStageDiscardsLateChecksumFailure(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Use the frozen independent corpus, never a private store layout or codec.
	data, err := os.ReadFile("../formatcontract/testdata/v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Vectors []struct {
			Name         string   `json:"name"`
			ID           string   `json:"id"`
			WireHex      string   `json:"wire_hex"`
			Dependencies []string `json:"dependencies"`
			Error        string   `json:"error"`
		}
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for i, v := range raw.Vectors {
		byName[v.Name] = i
	}
	var put func(string) storev2.ID
	put = func(name string) storev2.ID {
		v := raw.Vectors[byName[name]]
		for _, dep := range v.Dependencies {
			put(dep)
		}
		wire, e := hex.DecodeString(v.WireHex)
		if e != nil {
			t.Fatal(e)
		}
		id, e := storev2.ParseID(v.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.store.Put(context.Background(), id, bytes.NewReader(wire)); e != nil {
			t.Fatal(e)
		}
		return id
	}
	id := put("manifest-old")
	var wire bytes.Buffer
	if err = r.store.Copy(context.Background(), id, &wire); err != nil {
		t.Fatal(err)
	}
	// Canonical manifest remains structurally valid but lies about whole-file SHA.
	payload := bytes.Clone(wire.Bytes()[storev2.HeaderSize:])
	payload[8] ^= 1
	bad, err := storev2.NewID(storev2.Manifest, payload)
	if err != nil {
		t.Fatal(err)
	}
	header, err := storev2.Header(storev2.Manifest, uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.Put(context.Background(), bad, bytes.NewReader(append(header[:], payload...))); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err = r.stageFile(dir, storev2.TreeEntry{Path: "asset.bin", Manifest: bad}); err == nil {
		t.Fatal("bad checksum accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("provisional output retained: %v %v", entries, err)
	}
	write(t, r.root, "precious", []byte("unchanged"))
	if err = r.Restore("precious"); err == nil {
		t.Fatal("untracked restore accepted")
	}
	got, _ := os.ReadFile(filepath.Join(r.root, "precious"))
	if string(got) != "unchanged" {
		t.Fatal("untracked bytes changed")
	}
}
