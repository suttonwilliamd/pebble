// Package v1fixture builds only caller-owned disposable v1 histories. It is a
// test fixture writer, not a migration path or source-repository repair helper.
package v1fixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

type History struct {
	Refs    map[string]string
	Commits map[string]snapshot.Commit
	Trees   map[string][]snapshot.TreeEntry
	Blobs   map[string][]byte
}

// Frozen copies the checked-in compatibility payloads into a new disposable root.
func Frozen(root, frozenDirectory string) (*History, error) {
	r, err := snapshot.Init(root)
	if err != nil {
		return nil, err
	}
	h := &History{map[string]string{}, map[string]snapshot.Commit{}, map[string][]snapshot.TreeEntry{}, map[string][]byte{}}
	var frozenCommit string
	for _, name := range []string{"blob", "tree", "commit"} {
		data, err := os.ReadFile(filepath.Join(frozenDirectory, name+".json"))
		if err != nil {
			return nil, err
		}
		var obj snapshot.Object
		if err := json.Unmarshal(data, &obj); err != nil {
			return nil, err
		}
		path := filepath.Join(root, ".pebble", "objects", obj.Hash[:2], obj.Hash[2:])
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return nil, err
		}
		switch obj.Type {
		case snapshot.ObjectTypeBlob:
			h.Blobs[obj.Hash] = obj.Content
		case snapshot.ObjectTypeTree:
			var entries []snapshot.TreeEntry
			if err := json.Unmarshal(obj.Content, &entries); err != nil {
				return nil, err
			}
			h.Trees[obj.Hash] = entries
		case snapshot.ObjectTypeCommit:
			var commit snapshot.Commit
			if err := json.Unmarshal(obj.Content, &commit); err != nil {
				return nil, err
			}
			h.Commits[obj.Hash] = commit
			frozenCommit = obj.Hash
		}
	}
	h.Refs["heads/main"] = frozenCommit
	if err := r.SetRef("heads/main", frozenCommit); err != nil {
		return nil, err
	}
	return h, nil
}

// Build extends Frozen with deterministic mixed binary, shared-ancestor branch,
// repeated-path and empty-tree history, using only the supplied disposable root.
func Build(root, frozenDirectory string) (*History, error) {
	h, err := Frozen(root, frozenDirectory)
	if err != nil {
		return nil, err
	}
	r, err := snapshot.NewRepository(root)
	if err != nil {
		return nil, err
	}
	frozenCommit := h.Refs["heads/main"]
	mixed, err := testfixture.Mixed(32 << 10)
	if err != nil {
		return nil, err
	}
	serial := 0
	add := func(parent, message string, files []testfixture.File) (string, error) {
		entries := make([]snapshot.TreeEntry, 0, len(files))
		for _, file := range files {
			blob := snapshot.NewObject(snapshot.ObjectTypeBlob, file.Data)
			if err := r.StoreObject(blob); err != nil {
				return "", err
			}
			h.Blobs[blob.Hash] = file.Data
			entries = append(entries, snapshot.TreeEntry{Name: file.Path, Mode: "-rw-r--r--", Hash: blob.Hash, Type: snapshot.ObjectTypeBlob})
		}
		tree := snapshot.CreateTree(entries)
		if err := r.StoreObject(tree); err != nil {
			return "", err
		}
		h.Trees[tree.Hash] = entries
		// Signed pre-epoch seconds, subsecond precision and a non-hour offset.
		stamp := time.Unix(-1000+int64(serial), 123456789).In(time.FixedZone("fixture", -7*3600-30*60))
		serial++
		commit := snapshot.Commit{Tree: tree.Hash, Parent: parent, Author: "PB-08 fixture café", Email: "fixture@example.invalid", Timestamp: stamp, Message: message}
		data, err := json.Marshal(commit)
		if err != nil {
			return "", err
		}
		commit.Hash = snapshot.ComputeHash(data)
		data, err = json.Marshal(commit)
		if err != nil {
			return "", err
		}
		object := snapshot.NewObject(snapshot.ObjectTypeCommit, data)
		if err := r.StoreObject(object); err != nil {
			return "", err
		}
		h.Commits[object.Hash] = commit
		return object.Hash, nil
	}
	base, err := add(frozenCommit, "mixed shared ancestor\nsecond line\tkept", mixed)
	if err != nil {
		return nil, err
	}
	h.Refs["heads/original"] = base
	h.Refs["heads/frozen"] = frozenCommit
	parent := base
	for _, revision := range testfixture.Revisions(64 << 10) {
		files := append(append([]testfixture.File{}, mixed...), revision.File)
		parent, err = add(parent, revision.Name, files)
		if err != nil {
			return nil, err
		}
	}
	h.Refs["heads/main"] = parent
	feature, err := add(base, "divergent binary branch", []testfixture.File{{Path: "binary/repeated.bin", Data: []byte{0, 255, 128, '\r', '\n'}}})
	if err != nil {
		return nil, err
	}
	h.Refs["heads/feature/media"] = feature
	h.Refs["heads/same-tip"] = feature
	empty, err := add(base, "empty historical snapshot", nil)
	if err != nil {
		return nil, err
	}
	h.Refs["heads/empty-tree"] = empty
	for name, hash := range h.Refs {
		if err := r.SetRef(name, hash); err != nil {
			return nil, err
		}
	}
	// Working data and index cache are deliberately unrelated to HEAD. A
	// migration must read stored history, not snapshot or clean this state.
	if err := os.WriteFile(filepath.Join(root, "untracked.bin"), []byte("untouched\x00\xff"), 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, ".pebble", "index"), []byte("opaque legacy working cache\n"), 0644); err != nil {
		return nil, err
	}
	return h, nil
}
