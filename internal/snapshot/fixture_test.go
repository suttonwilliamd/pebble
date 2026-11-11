package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func assertFixtureFiles(t *testing.T, r *Repository, files []testfixture.File) {
	t.Helper()
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]TreeEntry{}
	for _, entry := range entries {
		byPath[entry.Name] = entry
	}
	for _, f := range files {
		entry, ok := byPath[f.Path]
		if !ok {
			t.Fatalf("missing committed file %s", f.Path)
		}
		obj, err := r.GetObject(entry.Hash)
		if err != nil || !bytes.Equal(obj.Content, f.Data) {
			t.Fatalf("stored bytes %s: %v", f.Path, err)
		}
		got, err := os.ReadFile(filepath.Join(r.RootPath(), filepath.FromSlash(f.Path)))
		if err != nil || !bytes.Equal(got, f.Data) {
			t.Fatalf("restored bytes %s: %v", f.Path, err)
		}
		delete(byPath, f.Path)
	}
	if len(byPath) != 0 {
		t.Fatalf("unexpected committed files: %v", byPath)
	}
}

func TestFixtureMixedHistoryReopenAndRestore(t *testing.T) {
	r := newTestRepo(t)
	files, err := testfixture.Mixed(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := testfixture.Materialize(r.RootPath(), files); err != nil {
		t.Fatal(err)
	}
	commitTest(t, r, "mixed corpus")
	revisions := testfixture.Revisions(64 << 10)
	var previous string
	for _, rev := range revisions {
		if err := testfixture.Materialize(r.RootPath(), []testfixture.File{rev.File}); err != nil {
			t.Fatal(err)
		}
		if rev.Name == "identical" {
			if _, err := r.Commit("unchanged", "fixture", "fixture@example.invalid"); err == nil {
				t.Fatal("unchanged revision committed")
			}
			hash, err := r.GetRef("heads/main")
			if err != nil || hash != previous {
				t.Fatal("unchanged revision moved ref")
			}
			continue
		}
		previous = commitTest(t, r, rev.Name)
		if err := r.CreateBranch("fixture-" + rev.Name); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(revisions) - 1; i >= 0; i-- {
		rev := revisions[i]
		if rev.Name == "identical" {
			continue
		}
		// Reopen each time: this must work from persisted history, not a cache.
		r, err = NewRepository(r.RootPath())
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Checkout("fixture-" + rev.Name); err != nil {
			t.Fatalf("checkout %s: %v", rev.Name, err)
		}
		want := append(append([]testfixture.File{}, files...), rev.File)
		assertFixtureFiles(t, r, want)
	}
	if _, err := r.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureFrozenJSONRepository(t *testing.T) {
	r := newTestRepo(t)
	for _, name := range []string{"blob", "tree", "commit"} {
		data, err := os.ReadFile(filepath.Join("testdata", "compat-v1", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var address struct {
			Hash string `json:"hash"`
		}
		if err := json.Unmarshal(data, &address); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(r.ObjectsPath(), address.Hash[:2], address.Hash[2:])
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	const commitHash = "266951d2dfb3b83ca5895b399ad602ebdaf743fa01c57dbc9d34fe58a2ea2e1b"
	// Install the frozen ref without using today's object serializer.
	if err := os.WriteFile(filepath.Join(r.refsPath, "heads", "legacy"), []byte(commitHash+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := NewRepository(r.RootPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("legacy"); err != nil {
		t.Fatal(err)
	}
	assertFixtureFiles(t, r, []testfixture.File{{Path: "legacy.bin", Data: []byte("legacy binary\x00\xff\r\n")}})
	if _, err := r.Verify(); err != nil {
		t.Fatal(err)
	}
	put(t, r, "legacy.bin", "new revision")
	next := commitTest(t, r, "continue frozen history")
	commit, err := r.GetCommit(next)
	if err != nil || commit.Parent != commitHash {
		t.Fatalf("compatibility history parent: %+v %v", commit, err)
	}
}

func TestFixtureMalformedObjects(t *testing.T) {
	valid := NewObject(ObjectTypeBlob, []byte("known bytes\x00\xff"))
	encode := func(obj Object) []byte {
		data, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	changed := func(change func(*Object)) []byte { obj := *valid; change(&obj); return encode(obj) }
	cases := []struct {
		name string
		data []byte
	}{
		{"truncated-json", []byte(`{"type":"blob"`)},
		{"invalid-base64", []byte(fmt.Sprintf(`{"type":"blob","size":13,"hash":%q,"content":"!!!"}`, valid.Hash))},
		{"negative-size", changed(func(o *Object) { o.Size = -1 })},
		{"wrong-size", changed(func(o *Object) { o.Size++ })},
		{"wrong-address", changed(func(o *Object) { o.Hash = ComputeHash([]byte("other")) })},
		{"wrong-content", changed(func(o *Object) { o.Content = []byte("tampered") })},
		{"unknown-type", changed(func(o *Object) { o.Type = "unknown" })},
		{"null-object", []byte("null")},
		{"missing-fields", []byte("{}")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t)
			path := filepath.Join(r.ObjectsPath(), valid.Hash[:2], valid.Hash[2:])
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.data, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := r.GetObject(valid.Hash); err == nil {
				t.Fatal("malformed object accepted")
			}
			if err := r.StoreObject(valid); err == nil {
				t.Fatal("existing malformed object hidden")
			}
		})
	}
}

func TestFixtureBlobReadFailure(t *testing.T) {
	data := testfixture.Noise(16 << 10)
	fault := errors.New("injected blob read failure")
	for _, offset := range []int{0, 1, 8193, len(data)} {
		obj, err := CreateBlobFromReader(testfixture.FailAfter(data, offset, fault))
		if !errors.Is(err, fault) || obj != nil {
			t.Fatalf("offset %d published partial blob: %+v %v", offset, obj, err)
		}
	}
}
