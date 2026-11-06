package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func bundleTestFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.pebble-bundle")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func repositoryFiles(t *testing.T, r *Repository) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(r.RootPath(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(r.RootPath(), path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBundleFileHistoryRoundTrip(t *testing.T) {
	source := newTestRepo(t)
	firstBytes := []byte{0, 255, 1, 128, '\r', '\n', 0}
	put(t, source, "assets/data.bin", string(firstBytes))
	put(t, source, "removed", "first only")
	first, err := source.Commit("first binary snapshot", "First Author", "first@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = source.CreateBranch("old"); err != nil {
		t.Fatal(err)
	}
	secondBytes := bytes.Repeat([]byte{255, 0, 42, 127, 128}, 1024)
	put(t, source, "assets/data.bin", string(secondBytes))
	put(t, source, "run.sh", "#!/bin/sh\nexit 0\n")
	if err = os.Chmod(filepath.Join(source.RootPath(), "run.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(source.RootPath(), "removed")); err != nil {
		t.Fatal(err)
	}
	second, err := source.Commit("second author and mode", "Second Author", "second@example.test")
	if err != nil {
		t.Fatal(err)
	}
	// Uncommitted bytes must never be substituted for immutable HEAD objects.
	put(t, source, "assets/data.bin", "uncommitted changes")
	put(t, source, "not-committed", "not in backup")
	path := filepath.Join(t.TempDir(), "history.bundle")
	beforeSource := repositoryFiles(t, source)
	if err = source.CreateBundleFile(path); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeSource, repositoryFiles(t, source)) {
		t.Fatal("export changed source repository")
	}
	// Transfer the file, not the source object directory, into an independent repo.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	transferred := bundleTestFile(t, data)
	dest := newTestRepo(t)
	put(t, dest, "local", "committed local history")
	local := commitTest(t, dest, "destination history")
	put(t, dest, "local", "dirty tracked data")
	put(t, dest, "untracked", "keep me")
	before := repositoryFiles(t, dest)
	if err = dest.ImportBundleFile(transferred, "recovered/main"); err != nil {
		t.Fatal(err)
	}
	after := repositoryFiles(t, dest)
	for name, want := range before {
		if after[name] != want {
			t.Fatalf("import changed existing file %s", name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok && !strings.HasPrefix(name, ".pebble/objects/") && name != ".pebble/refs/heads/recovered/main" {
			t.Fatalf("unexpected import side effect: %s", name)
		}
	}
	if head, _ := dest.GetHead(); head != "heads/main" {
		t.Fatal("import changed HEAD")
	}
	if hash, _ := dest.GetRef("heads/main"); hash != local {
		t.Fatal("import changed destination history")
	}
	want, err := source.Export("heads/main")
	if err != nil {
		t.Fatal(err)
	}
	got, err := dest.Export("heads/recovered/main")
	if err != nil {
		t.Fatal(err)
	}
	want.Ref = got.Ref
	if !reflect.DeepEqual(want, got) || got.Hash != second {
		t.Fatal("roundtrip changed typed objects, hashes or history metadata")
	}
	c, err := dest.GetCommit(second)
	if err != nil || c.Parent != first || c.Author != "Second Author" || c.Email != "second@example.test" {
		t.Fatalf("lost commit metadata: %+v %v", c, err)
	}
	if _, err = dest.Verify(); err != nil {
		t.Fatal(err)
	}
	// Explicit checkout remains guarded independently of import.
	if err = dest.Checkout("recovered/main"); err == nil {
		t.Fatal("checkout discarded dirty work")
	}
	if err = dest.Restore("local"); err != nil {
		t.Fatal(err)
	}
	if err = dest.Checkout("recovered/main"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(content(t, dest, "assets/data.bin")), secondBytes) {
		t.Fatal("tip binary content changed")
	}
	if _, err = os.Stat(filepath.Join(dest.RootPath(), "not-committed")); !os.IsNotExist(err) {
		t.Fatal("export included uncommitted file")
	}
	if content(t, dest, "untracked") != "keep me" {
		t.Fatal("checkout removed unrelated untracked file")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dest.RootPath(), "run.sh"))
		if err != nil || info.Mode()&0111 == 0 {
			t.Fatalf("executable mode not restored: %v", err)
		}
	}
	if err = dest.CreateBranch("first"); err != nil {
		t.Fatal(err)
	}
	if err = dest.SetRef("heads/first", first); err != nil {
		t.Fatal(err)
	}
	if err = dest.Checkout("first"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(content(t, dest, "assets/data.bin")), firstBytes) || content(t, dest, "removed") != "first only" {
		t.Fatal("older binary snapshot not recoverable")
	}
}

func TestBundleFileInvalidNeverWrites(t *testing.T) {
	source := newTestRepo(t)
	put(t, source, "asset", "original")
	commitTest(t, source, "initial")
	base, err := source.Export("heads/main")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"empty":                nil,
		"truncated":            valid[:len(valid)-1],
		"trailing JSON":        append(append([]byte{}, valid...), []byte(" {}")...),
		"trailing garbage":     append(append([]byte{}, valid...), 'x'),
		"unknown field":        append([]byte(`{"unexpected":true,`), valid[1:]...),
		"null":                 []byte("null"),
		"root array":           []byte("[]"),
		"duplicate field":      append([]byte(`{"version":1,`), valid[1:]...),
		"duplicate array":      append([]byte(`{"objects":[],`), valid[1:]...),
		"unknown object field": bytes.Replace(valid, []byte(`"type":`), []byte(`"unexpected":true,"type":`), 1),
	}
	mutations := map[string]func(*Bundle){
		"version":                func(b *Bundle) { b.Version++ },
		"source ref":             func(b *Bundle) { b.Ref = "heads/../unsafe" },
		"tip":                    func(b *Bundle) { b.Hash = strings.Repeat("0", 64) },
		"content":                func(b *Bundle) { b.Objects[0].Content = []byte("tampered") },
		"size":                   func(b *Bundle) { b.Objects[0].Size++ },
		"missing parent or tree": func(b *Bundle) { b.Objects = b.Objects[1:] },
		"duplicate object":       func(b *Bundle) { b.Objects = append(b.Objects, b.Objects[0]) },
		"unreachable object":     func(b *Bundle) { b.Objects = append(b.Objects, NewObject(ObjectTypeBlob, []byte("unreachable"))) },
		"too many objects": func(b *Bundle) {
			obj := b.Objects[0]
			b.Objects = make([]*Object, MaxTransferObjects+1)
			for i := range b.Objects {
				b.Objects[i] = obj
			}
		},
	}
	for name, mutate := range mutations {
		b := copyBundle(t, base)
		mutate(b)
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		cases[name] = data
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			r := newTestRepo(t)
			put(t, r, "keep", "untracked work")
			before := repositoryFiles(t, r)
			err := r.ImportBundleFile(bundleTestFile(t, data), "restored")
			if !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("expected invalid bundle: %v", err)
			}
			if !reflect.DeepEqual(before, repositoryFiles(t, r)) {
				t.Fatal("invalid file wrote objects, refs or working files")
			}
		})
	}
}

func TestBundleFileNoOverwriteAndLocking(t *testing.T) {
	source := newTestRepo(t)
	commitTest(t, source, "empty snapshot is valid history")
	path := filepath.Join(t.TempDir(), "history.bundle")
	if err := source.CreateBundleFile(path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.CreateBundleFile(path); err == nil {
		t.Fatal("overwrote bundle")
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, original) {
		t.Fatal("existing bundle changed")
	}
	for _, path := range []string{source.headPath, filepath.Join(source.refsPath, "heads", "new"), filepath.Join(source.RootPath(), ".git", "new")} {
		if err = source.CreateBundleFile(path); err == nil {
			t.Fatalf("wrote metadata: %s", path)
		}
	}
	for _, branch := range []string{"main", "Main", "main/nested", "../escape", "CON", ""} {
		r := newTestRepo(t)
		before := repositoryFiles(t, r)
		if err = r.ImportBundleFile(path, branch); err == nil {
			t.Fatalf("accepted branch %q", branch)
		}
		if !reflect.DeepEqual(before, repositoryFiles(t, r)) {
			t.Fatalf("branch refusal changed repository: %q", branch)
		}
	}
	dest := newTestRepo(t)
	if err = dest.CreateBranch("empty"); err != nil {
		t.Fatal(err)
	}
	if err = dest.ImportBundleFile(path, "empty"); err == nil {
		t.Fatal("overwrote existing empty branch")
	}
	if err = dest.ImportBundleFile(path, "backup"); err != nil {
		t.Fatal(err)
	}
	before := repositoryFiles(t, dest)
	if err = dest.ImportBundleFile(path, "backup"); err == nil {
		t.Fatal("overwrote existing populated branch")
	}
	if !reflect.DeepEqual(before, repositoryFiles(t, dest)) {
		t.Fatal("repeated import changed repository")
	}
	unlock, err := dest.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if err = dest.ImportBundleFile(path, "locked"); err == nil {
		t.Fatal("import ignored writer lock")
	}
	unlock()
	unlock, err = source.Lock()
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(t.TempDir(), "locked.bundle")
	if err = source.CreateBundleFile(locked); err == nil {
		t.Fatal("create ignored writer lock")
	}
	unlock()
	if _, err = os.Stat(locked); !os.IsNotExist(err) {
		t.Fatal("locked create left output")
	}
}

func TestBundleFileEmptyAndCorruptHistory(t *testing.T) {
	r := newTestRepo(t)
	path := filepath.Join(t.TempDir(), "empty.bundle")
	if err := r.CreateBundleFile(path); err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("unborn history: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("empty export left a file")
	}
	put(t, r, "a", "original")
	first := commitTest(t, r, "first")
	put(t, r, "a", "new")
	commitTest(t, r, "second")
	// A valid tip is insufficient: the older parent must also exist and verify.
	objectPath := filepath.Join(r.objectsPath, first[:2], first[2:])
	original, err := os.ReadFile(objectPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			if err = os.WriteFile(objectPath, []byte("corrupt"), 0644); err != nil {
				t.Fatal(err)
			}
		} else if err = os.Remove(objectPath); err != nil {
			t.Fatal(err)
		}
		if err = r.CreateBundleFile(path); err == nil {
			t.Fatal("export accepted incomplete/corrupt ancestor")
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid export left a file")
		}
		if err = os.WriteFile(objectPath, original, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBundleFileByteLimits(t *testing.T) {
	source := newTestRepo(t)
	commitTest(t, source, "empty tree")
	b, err := source.Export("heads/main")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	// The complete encoded file, including otherwise legal whitespace, is bounded.
	atLimit := append(data, bytes.Repeat([]byte(" "), int(MaxBundleBytes)-len(data))...)
	path := bundleTestFile(t, atLimit)
	dest := newTestRepo(t)
	if err = dest.ImportBundleFile(path, "at-limit"); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	before := repositoryFiles(t, dest)
	if err = dest.ImportBundleFile(path, "over-limit"); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("accepted oversized file: %v", err)
	}
	if !reflect.DeepEqual(before, repositoryFiles(t, dest)) {
		t.Fatal("oversized import changed repository")
	}
	if err = dest.ImportBundleFile(t.TempDir(), "directory"); err == nil {
		t.Fatal("accepted directory bundle")
	}
	// A 24 MiB blob alone expands to 32 MiB base64, leaving no room for metadata.
	blob := NewObject(ObjectTypeBlob, make([]byte, 24<<20))
	tree := CreateTree([]TreeEntry{{Name: "huge.bin", Mode: "-rw-r--r--", Hash: blob.Hash, Type: ObjectTypeBlob}})
	commit := CreateCommit(tree.Hash, "", "Size Test", "size@example.test", "oversized")
	for _, obj := range []*Object{blob, tree, commit} {
		if err = source.StoreObject(obj); err != nil {
			t.Fatal(err)
		}
	}
	if err = source.SetRef("heads/main", commit.Hash); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "too-large.bundle")
	if err = source.CreateBundleFile(output); err == nil {
		t.Fatal("export exceeded encoded limit")
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("oversized export left output")
	}
}

func TestBundleFilePreservesPortableExecutableMode(t *testing.T) {
	source := newTestRepo(t)
	blob := NewObject(ObjectTypeBlob, []byte("#!/bin/sh\nexit 0\n"))
	tree := CreateTree([]TreeEntry{{Name: "run.sh", Mode: "-rwxr-xr-x", Hash: blob.Hash, Type: ObjectTypeBlob}})
	commit := CreateCommit(tree.Hash, "", "Unix Author", "unix@example.test", "executable")
	for _, obj := range []*Object{blob, tree, commit} {
		if err := source.StoreObject(obj); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.SetRef("heads/main", commit.Hash); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mode.bundle")
	if err := source.CreateBundleFile(path); err != nil {
		t.Fatal(err)
	}
	dest := newTestRepo(t)
	if err := dest.ImportBundleFile(path, "unix"); err != nil {
		t.Fatal(err)
	}
	entries, err := dest.treeForCommit(commit.Hash)
	if err != nil || len(entries) != 1 || entries[0].Mode != "-rwxr-xr-x" {
		t.Fatalf("lost portable mode: %v %v", entries, err)
	}
	if err = dest.Checkout("unix"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dest.RootPath(), "run.sh"))
		if err != nil || info.Mode()&0111 == 0 {
			t.Fatalf("lost executable checkout mode: %v", err)
		}
	}
}

// The guard errors if decoding reaches the unfinished hostile value. Short reads
// keep decoder read-ahead bounded without relying on its internal buffer size.
type guardedBundleReader struct {
	prefix       string
	reachedGuard bool
}

func (r *guardedBundleReader) Read(p []byte) (int, error) {
	if len(r.prefix) == 0 {
		r.reachedGuard = true
		return 0, errors.New("read past hostile-value guard")
	}
	if len(p) > 64 {
		p = p[:64]
	}
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, nil
}

func TestBundleDecoderRejectsBeforeReadingPastLimits(t *testing.T) {
	tests := map[string]struct{ prefix, message string }{
		"object count": {
			`{"objects":[` + strings.Repeat("{},", MaxTransferObjects) + `{"content":"` + strings.Repeat("x", 4096),
			"too many objects",
		},
		"trailing array": {
			`{} [` + strings.Repeat("0,", MaxTransferObjects),
			"expected exactly one JSON bundle",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			reader := &guardedBundleReader{prefix: test.prefix}
			if _, err := decodeBundle(reader); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected early rejection %q, got %v", test.message, err)
			}
			if reader.reachedGuard {
				t.Fatal("decoder read past the bound into the hostile value")
			}
		})
	}
}

func TestBundleFileRejectsFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO creation is unavailable on Windows")
	}
	if path := os.Getenv("PEBBLE_TEST_FIFO_CHILD"); path != "" {
		r := newTestRepo(t)
		before := repositoryFiles(t, r)
		err := r.ImportBundleFile(path, "fifo")
		if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("expected FIFO rejection, got %v", err)
		}
		if !reflect.DeepEqual(before, repositoryFiles(t, r)) {
			t.Fatal("FIFO rejection changed repository")
		}
		return
	}
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo is unavailable")
	}
	path := filepath.Join(t.TempDir(), "backup.fifo")
	if output, err := exec.Command(mkfifo, path).CombinedOutput(); err != nil {
		t.Fatalf("creating test FIFO: %v: %s", err, output)
	}
	alias := path + "-alias"
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{path, alias} {
		t.Run(filepath.Base(input), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBundleFileRejectsFIFO$")
			cmd.Env = append(os.Environ(), "PEBBLE_TEST_FIFO_CHILD="+input)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("FIFO import blocked instead of rejecting: %v: %s", ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("FIFO rejection subprocess: %v: %s", err, output)
			}
		})
	}
}
