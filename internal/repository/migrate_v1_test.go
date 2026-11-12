package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/storev2"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
	v1fixture "github.com/suttonwilliamd/pebble/internal/testfixture/v1"
)

func migrationBase(t *testing.T) string {
	t.Helper()
	// macOS's system temp path may itself contain /var -> /private/var.
	// Pass the real disposable root; production intentionally rejects aliases.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func frozenMigration(t *testing.T) (string, string, *v1fixture.History) {
	t.Helper()
	base := migrationBase(t)
	source := filepath.Join(base, "source")
	h, err := v1fixture.Frozen(source, filepath.Join("..", "snapshot", "testdata", "compat-v1"))
	if err != nil {
		t.Fatal(err)
	}
	return source, filepath.Join(base, "destination"), h
}

func sourceState(t *testing.T, root string) map[string]v1fixture.FileState {
	t.Helper()
	state, err := v1fixture.Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func unchangedSource(t *testing.T, root string, before map[string]v1fixture.FileState) {
	t.Helper()
	if after := sourceState(t, root); !reflect.DeepEqual(before, after) {
		for path, want := range before {
			if got, ok := after[path]; !ok || !reflect.DeepEqual(want, got) {
				t.Errorf("source state differs at %s: before=%+v after=%+v present=%v", path, want, got, ok)
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				t.Errorf("source gained path %s", path)
			}
		}
		t.Fatal("migration changed source bytes, refs, HEAD, working files, modes or modification times")
	}
}
func mappedID(t *testing.T, result *MigrationResult, hash string) storev2.ID {
	t.Helper()
	id, err := storev2.ParseID(result.Objects[hash])
	if err != nil {
		t.Fatalf("missing/invalid mapping for %s: %v", hash, err)
	}
	return id
}

func assertMigratedHistory(t *testing.T, destination string, h *v1fixture.History, result *MigrationResult, selected []string) {
	t.Helper()
	ctx := context.Background()
	s, err := storev2.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.ListRefs(ctx)
	if err != nil || len(refs) != len(selected) {
		t.Fatalf("destination refs: %v %v", refs, err)
	}
	reachable := map[string]bool{}
	pending := []string{}
	for _, branch := range selected {
		hash := h.Refs[branch]
		pending = append(pending, hash)
		if refs[branch] != mappedID(t, result, hash) || result.Branches[branch] != refs[branch].String() {
			t.Fatalf("branch mapping differs: %s", branch)
		}
	}
	for len(pending) > 0 {
		hash := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[hash] {
			continue
		}
		reachable[hash] = true
		original := h.Commits[hash]
		commit, err := s.ReadCommit(ctx, mappedID(t, result, hash))
		if err != nil {
			t.Fatal(err)
		}
		if commit.Tree != mappedID(t, result, original.Tree) || commit.Author != original.Author || commit.Email != original.Email || commit.Message != original.Message || commit.Seconds != original.Timestamp.Unix() || commit.Nanoseconds != uint32(original.Timestamp.Nanosecond()) || commit.Offset != original.Timestamp.Format("-0700") {
			t.Fatalf("metadata/graph lost for v1 commit %s", hash)
		}
		if original.Parent == "" {
			if len(commit.Parents) != 0 {
				t.Fatal("invented root ancestry")
			}
		} else {
			if len(commit.Parents) != 1 || commit.Parents[0] != mappedID(t, result, original.Parent) {
				t.Fatalf("parent lost for %s", hash)
			}
			pending = append(pending, original.Parent)
		}
		reachable[original.Tree] = true
		entries, err := s.ReadTree(ctx, commit.Tree)
		if err != nil || len(entries) != len(h.Trees[original.Tree]) {
			t.Fatalf("tree mismatch: %v", err)
		}
		for i, entry := range h.Trees[original.Tree] {
			reachable[entry.Hash] = true
			got := entries[i]
			if got.Path != entry.Name || got.Manifest != mappedID(t, result, entry.Hash) || got.Executable != (entry.Mode == "-rwxr-xr-x") {
				t.Fatalf("tree metadata/mapping lost for %s:%s", hash, entry.Name)
			}
			var restored bytes.Buffer
			if err := s.CopyFile(ctx, got.Manifest, &restored); err != nil {
				t.Fatal(err)
			}
			want := h.Blobs[entry.Hash]
			length, sum, err := s.FileInfo(ctx, got.Manifest)
			if err != nil || length != uint64(len(want)) || sum != sha256.Sum256(want) || !bytes.Equal(restored.Bytes(), want) || sha256.Sum256(restored.Bytes()) != sum {
				t.Fatalf("historical restored bytes/checksum mismatch at %s:%s: %v", hash, entry.Name, err)
			}
		}
	}
	if len(reachable) != len(result.Objects) {
		t.Fatalf("mapping includes missing or unselected objects: want %d got %d", len(reachable), len(result.Objects))
	}
	for hash := range result.Objects {
		if !reachable[hash] {
			t.Fatalf("unselected mapping: %s", hash)
		}
	}
	data, err := os.ReadFile(filepath.Join(destination, ".pebble", "migration-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt MigrationResult
	if err := json.Unmarshal(data, &receipt); err != nil || !reflect.DeepEqual(&receipt, result) {
		t.Fatalf("durable mapping receipt differs: %v", err)
	}
	r, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	if head, err := r.GetHead(); err != nil || head != result.Head {
		t.Fatalf("HEAD mismatch: %s %v", head, err)
	}
	if changes, err := r.Status(); err != nil || len(changes) != 0 {
		t.Fatalf("destination not immediately usable/clean: %v %v", changes, err)
	}
	for _, branch := range selected {
		if err := r.Checkout(strings.TrimPrefix(branch, "heads/")); err != nil {
			t.Fatal(err)
		}
		for _, entry := range h.Trees[h.Commits[h.Refs[branch]].Tree] {
			got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(entry.Name)))
			if err != nil || !bytes.Equal(got, h.Blobs[entry.Hash]) {
				t.Fatalf("branch checkout mismatch: %s:%s %v", branch, entry.Name, err)
			}
		}
	}
	if _, err := r.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationAllAndSelectedReachableHistory(t *testing.T) {
	base := migrationBase(t)
	source := filepath.Join(base, "source")
	h, err := v1fixture.Build(source, filepath.Join("..", "snapshot", "testdata", "compat-v1"))
	if err != nil {
		t.Fatal(err)
	}
	before := sourceState(t, source)
	for _, tc := range []struct {
		name     string
		options  MigrationOptions
		branches []string
	}{
		{"all", MigrationOptions{}, sortedV1Refs(h.Refs)},
		{"selected", MigrationOptions{Branches: []string{"feature/media", "frozen"}, Head: "feature/media"}, []string{"heads/feature/media", "heads/frozen"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := filepath.Join(base, tc.name)
			result, err := MigrateV1(context.Background(), source, destination, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			assertMigratedHistory(t, destination, h, result, tc.branches)
			unchangedSource(t, source, before)
		})
	}
}

func TestMigrationEmptyHistoryAndSelectionGuards(t *testing.T) {
	base := migrationBase(t)
	source := filepath.Join(base, "source")
	if _, err := snapshot.Init(source); err != nil {
		t.Fatal(err)
	}
	before := sourceState(t, source)
	destination := filepath.Join(base, "empty")
	result, err := MigrateV1(context.Background(), source, destination, MigrationOptions{})
	if err != nil || result == nil || len(result.Objects) != 0 || result.Head != "heads/main" || result.Branches["heads/main"] != "" {
		t.Fatalf("unborn migration: %+v %v", result, err)
	}
	r, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	if changes, err := r.Status(); err != nil || len(changes) != 0 {
		t.Fatalf("unborn destination: %v %v", changes, err)
	}
	unchangedSource(t, source, before)
	for _, options := range []MigrationOptions{
		{Branches: []string{"missing"}}, {Branches: []string{"main", "main"}}, {Branches: []string{"../main"}}, {Head: "other"},
	} {
		if got, err := MigrateV1(context.Background(), source, filepath.Join(base, "rejected"), options); err == nil || got != nil {
			t.Fatalf("invalid selection accepted: %+v", options)
		}
		unchangedSource(t, source, before)
	}
	source, destination, _ = frozenMigration(t)
	if err := os.WriteFile(filepath.Join(source, ".pebble", "refs", "heads", "unborn"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	assertMigrationRejected(t, source, destination, MigrationOptions{}, nil)
	// Selecting a non-HEAD branch without an explicit HEAD is ambiguous.
	assertMigrationRejected(t, source, destination, MigrationOptions{Branches: []string{"unborn"}}, nil)
}

func assertMigrationRejected(t *testing.T, source, destination string, options MigrationOptions, beforePublish func() error) {
	t.Helper()
	before := sourceState(t, source)
	result, err := migrateV1(context.Background(), source, destination, options, beforePublish)
	if err == nil || result != nil {
		t.Fatalf("incomplete/corrupt migration acknowledged: %+v %v", result, err)
	}
	unchangedSource(t, source, before)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed migration published destination: %v", err)
	}
	stages, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".pebble-migrate-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("ordinary failure leaked stage: %v %v", stages, err)
	}
}

func storeMigrationCommit(t *testing.T, source string, c snapshot.Commit) string {
	t.Helper()
	c.Hash = ""
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := snapshot.NewRepository(source)
	if err != nil {
		t.Fatal(err)
	}
	obj := snapshot.NewObject(snapshot.ObjectTypeCommit, data)
	if err := r.StoreObject(obj); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRef("heads/main", obj.Hash); err != nil {
		t.Fatal(err)
	}
	return obj.Hash
}

func TestMigrationRejectsCorruptIncompleteAndUnsupportedInput(t *testing.T) {
	cases := []string{"missing-commit", "missing-tree", "missing-blob", "wrong-size", "wrong-address", "wrong-kind", "unknown-envelope-field", "unknown-parent-metadata", "missing-identity", "corrupt-embedded-hash", "locked", "marked", "unsupported-refs", "oversized", "fault-prefix-0", "fault-prefix-half", "fault-prefix-final"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			source, destination, h := frozenMigration(t)
			tip := h.Refs["heads/main"]
			c := h.Commits[tip]
			blobHash := h.Trees[c.Tree][0].Hash
			objectPath := func(hash string) string { return filepath.Join(source, ".pebble", "objects", hash[:2], hash[2:]) }
			path := objectPath(tip)
			switch name {
			case "missing-commit", "missing-tree", "missing-blob":
				if name == "missing-tree" {
					path = objectPath(c.Tree)
				}
				if name == "missing-blob" {
					path = objectPath(blobHash)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "missing-identity":
				c.Author = ""
				storeMigrationCommit(t, source, c)
			case "locked":
				if err := os.WriteFile(filepath.Join(source, ".pebble", "write.lock"), []byte("preserve lock"), 0600); err != nil {
					t.Fatal(err)
				}
			case "marked":
				if err := os.WriteFile(filepath.Join(source, ".pebble", "format"), []byte("pebble-format 2\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "unsupported-refs":
				if err := os.Mkdir(filepath.Join(source, ".pebble", "refs", "tags"), 0755); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				f, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := errors.Join(f.Truncate(MaxV1EnvelopeBytes+1), f.Close()); err != nil {
					t.Fatal(err)
				}
			default:
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var obj snapshot.Object
				if err := json.Unmarshal(data, &obj); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "wrong-size":
					obj.Size++
				case "wrong-address":
					obj.Hash = strings.Repeat("a", 64)
				case "wrong-kind":
					obj.Type = snapshot.ObjectTypeBlob
				case "unknown-parent-metadata":
					obj.Content = append(append([]byte{}, obj.Content[:len(obj.Content)-1]...), []byte(",\"parents\":[\"unsupported\"]}")...)
					obj = *snapshot.NewObject(snapshot.ObjectTypeCommit, obj.Content)
					path = objectPath(obj.Hash)
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(source, ".pebble", "refs", "heads", "main"), []byte(obj.Hash+"\n"), 0644); err != nil {
						t.Fatal(err)
					}
				case "corrupt-embedded-hash":
					c.Hash = strings.Repeat("a", 64)
					obj.Content, err = json.Marshal(c)
					if err != nil {
						t.Fatal(err)
					}
					obj = *snapshot.NewObject(snapshot.ObjectTypeCommit, obj.Content)
					path = objectPath(obj.Hash)
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(source, ".pebble", "refs", "heads", "main"), []byte(obj.Hash+"\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				data, err = json.Marshal(obj)
				if err != nil {
					t.Fatal(err)
				}
				if name == "unknown-envelope-field" {
					data = append(append([]byte{}, data[:len(data)-1]...), []byte(",\"signature\":\"do not discard\"}")...)
				}
				if strings.HasPrefix(name, "fault-prefix-") {
					offset := 0
					if name == "fault-prefix-half" {
						offset = len(data) / 2
					}
					if name == "fault-prefix-final" {
						offset = len(data) - 1
					}
					fault := errors.New("fixture read interrupted")
					data, err = io.ReadAll(testfixture.FailAfter(data, offset, fault))
					if !errors.Is(err, fault) {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			assertMigrationRejected(t, source, destination, MigrationOptions{}, nil)
		})
	}
}

func TestMigrationRejectsUnsafeTreeAndModes(t *testing.T) {
	for _, names := range [][]string{{"../escape"}, {"bad\\name"}, {"CON"}, {"a", "a/b"}, {"Case/a", "case/b"}, {"same", "same"}} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			source, destination, h := frozenMigration(t)
			c := h.Commits[h.Refs["heads/main"]]
			r, err := snapshot.NewRepository(source)
			if err != nil {
				t.Fatal(err)
			}
			entries := []snapshot.TreeEntry{}
			for _, name := range names {
				entries = append(entries, snapshot.TreeEntry{Name: name, Mode: "-rw-r--r--", Type: snapshot.ObjectTypeBlob, Hash: h.Trees[c.Tree][0].Hash})
			}
			tree := snapshot.CreateTree(entries)
			if err := r.StoreObject(tree); err != nil {
				t.Fatal(err)
			}
			c.Tree = tree.Hash
			storeMigrationCommit(t, source, c)
			assertMigrationRejected(t, source, destination, MigrationOptions{}, nil)
		})
	}
	for _, mode := range []string{"-rw-------", "-rw-rw-rw-", "lrwxrwxrwx", "100644"} {
		t.Run(mode, func(t *testing.T) {
			source, destination, h := frozenMigration(t)
			c := h.Commits[h.Refs["heads/main"]]
			entries := append([]snapshot.TreeEntry{}, h.Trees[c.Tree]...)
			entries[0].Mode = mode
			tree := snapshot.CreateTree(entries)
			r, err := snapshot.NewRepository(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.StoreObject(tree); err != nil {
				t.Fatal(err)
			}
			c.Tree = tree.Hash
			storeMigrationCommit(t, source, c)
			assertMigrationRejected(t, source, destination, MigrationOptions{}, nil)
		})
	}
}

func TestMigrationDestinationOverlapAndAliasGuards(t *testing.T) {
	source, destination, _ := frozenMigration(t)
	before := sourceState(t, source)
	for _, target := range []string{source, filepath.Join(source, "inside"), filepath.Dir(source)} {
		if result, err := MigrateV1(context.Background(), source, target, MigrationOptions{}); err == nil || result != nil {
			t.Fatalf("overlap accepted: %s", target)
		}
		unchangedSource(t, source, before)
	}
	for _, directory := range []bool{true, false} {
		if directory {
			if err := os.Mkdir(destination, 0755); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(destination, []byte("unrelated"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		want := sourceState(t, filepath.Dir(source))
		if result, err := MigrateV1(context.Background(), source, destination, MigrationOptions{}); err == nil || result != nil {
			t.Fatal("existing destination accepted")
		}
		if got := sourceState(t, filepath.Dir(source)); !reflect.DeepEqual(want, got) {
			t.Fatal("existing destination overwritten")
		}
		if err := os.Remove(destination); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("symlink-alias", func(t *testing.T) {
		alias := filepath.Join(filepath.Dir(source), "alias")
		if err := os.Symlink(source, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		for _, pair := range [][2]string{{alias, destination}, {source, filepath.Join(alias, "child")}} {
			if result, err := MigrateV1(context.Background(), pair[0], pair[1], MigrationOptions{}); err == nil || result != nil {
				t.Fatal("symlink alias accepted")
			}
		}
		unchangedSource(t, source, before)
	})
}

func TestMigrationCancellationAndPublicationRace(t *testing.T) {
	source, destination, _ := frozenMigration(t)
	before := sourceState(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	result, err := migrateV1(ctx, source, destination, MigrationOptions{}, func() error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("canceled migration acknowledged: %+v %v", result, err)
	}
	unchangedSource(t, source, before)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("cancellation exposed partial destination")
	}
	fault := errors.New("publication interrupted")
	assertMigrationRejected(t, source, destination, MigrationOptions{}, func() error { return fault })
	result, err = migrateV1(context.Background(), source, destination, MigrationOptions{}, func() error { return os.WriteFile(destination, []byte("other publisher"), 0644) })
	if err == nil || result != nil {
		t.Fatal("publication race acknowledged")
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "other publisher" {
		t.Fatalf("race overwrote unrelated destination: %q %v", data, err)
	}
	unchangedSource(t, source, before)
}

func TestMigrationDetectsChangedSourceRefs(t *testing.T) {
	source, destination, h := frozenMigration(t)
	var expected map[string]v1fixture.FileState
	result, err := migrateV1(context.Background(), source, destination, MigrationOptions{}, func() error {
		path := filepath.Join(source, ".pebble", "refs", "heads", "added")
		if err := os.WriteFile(path, []byte(h.Refs["heads/main"]+"\n"), 0644); err != nil {
			return err
		}
		expected = sourceState(t, source)
		return nil
	})
	if err == nil || result != nil {
		t.Fatal("changed source refs acknowledged")
	}
	unchangedSource(t, source, expected)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("changed-source destination published")
	}
}

func TestMigrationExclusivePublicationPreservesExistingDirectories(t *testing.T) {
	base := migrationBase(t)
	stage, destination := filepath.Join(base, "stage"), filepath.Join(base, "destination")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "data"), []byte("staged"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		t.Fatal(err)
	}
	// An ordinary POSIX rename can replace an empty directory. Our actual
	// namespace primitive must refuse it even after the high-level guard races.
	if err := publishMigration(stage, destination); err == nil {
		t.Fatal("publication replaced existing empty directory")
	}
	if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(stage, "data")); err != nil || string(data) != "staged" {
		t.Fatal("failed publication damaged stage")
	}
}

func TestMigrationInterruption(t *testing.T) {
	if source := os.Getenv("PEBBLE_TEST_V1_CRASH_SOURCE"); source != "" {
		_, err := migrateV1(context.Background(), source, os.Getenv("PEBBLE_TEST_V1_CRASH_DEST"), MigrationOptions{}, func() error { os.Exit(37); return nil })
		t.Fatalf("did not reach publication boundary: %v", err)
	}
	source, destination, _ := frozenMigration(t)
	before := sourceState(t, source)
	command := exec.Command(os.Args[0], "-test.run=^TestMigrationInterruption$")
	command.Env = append(os.Environ(), "PEBBLE_TEST_V1_CRASH_SOURCE="+source, "PEBBLE_TEST_V1_CRASH_DEST="+destination)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 37 {
		t.Fatalf("interruption helper: %v\n%s", err, output)
	}
	unchangedSource(t, source, before)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("interrupted migration exposed destination")
	}
	stages, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".pebble-migrate-*", "repository", ".pebble", "migration-v1.json"))
	if err != nil || len(stages) != 1 {
		t.Fatalf("expected private interrupted stage, not an acknowledged destination: %v %v", stages, err)
	}
}
