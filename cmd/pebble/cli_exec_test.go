package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

// Execute the shipped program, reopening the repository for every operation.
func TestExecutableMixedHistory(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pebble")
	goName := "go"
	if runtime.GOOS == "windows" {
		bin += ".exe"
		goName += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := t.TempDir()
	env := append(isolatedIdentityEnv(t), "PEBBLE_AUTHOR=CLI Fixture", "PEBBLE_EMAIL=fixture@example.com")
	invoke := func(ok bool, args ...string) string {
		t.Helper()
		c := exec.Command(bin, args...)
		c.Dir = root
		c.Env = env
		out, err := c.CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	files, err := testfixture.Mixed(16 << 10)
	if err != nil {
		t.Fatal(err)
	}
	revisions := testfixture.Revisions(32 << 10)
	if err = testfixture.Materialize(root, append(files, revisions[0].File)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".pebbleignore"), []byte("newfile\n"), 0644); err != nil {
		t.Fatal(err)
	}
	invoke(true, "init") // Existing mixed user files must survive initialization.
	assertFiles := func(want []testfixture.File) {
		t.Helper()
		for _, f := range want {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, f.Data) || sha256.Sum256(got) != sha256.Sum256(f.Data) {
				t.Fatalf("byte/checksum mismatch: %s", f.Path)
			}
		}
	}
	assertFiles(files)
	invoke(true, "add", ".")
	invoke(true, "commit", "initial")
	invoke(true, "branch", "original")
	for _, rev := range revisions[1:] {
		if err = testfixture.Materialize(root, []testfixture.File{rev.File}); err != nil {
			t.Fatal(err)
		}
		if rev.Name == "identical" {
			invoke(false, "commit", rev.Name)
			continue
		}
		invoke(true, "add", ".")
		invoke(true, "commit", rev.Name)
		invoke(true, "branch", rev.Name)
		if s := invoke(true, "status"); !strings.Contains(s, "Working tree clean") {
			t.Fatal(s)
		}
	}
	// Exercise the public physical backend without reading or altering its
	// private layout. Pack preserves loose copies; every reopened CLI read
	// must still validate packed representations as well.
	store, err := storev2.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pack(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	invoke(true, "fsck")
	history := invoke(true, "log")
	for _, word := range []string{"initial", "edited", "inserted", "truncated", "empty"} {
		if !strings.Contains(history, word) {
			t.Fatalf("missing history %s", word)
		}
	}
	if !strings.Contains(invoke(true, "branch"), "original") {
		t.Fatal("missing branch")
	}
	for _, rev := range revisions {
		branch := rev.Name
		if branch == "identical" {
			continue
		}
		invoke(true, "checkout", branch)
		assertFiles(append(files, rev.File))
		if s := invoke(true, "status"); !strings.Contains(s, "Working tree clean") {
			t.Fatal(s)
		}
		// Restore each actual encoded asset and repeated-path revision byte-for-byte.
		for _, f := range append(files, rev.File) {
			if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(f.Path)), []byte("discard me"), 0644); err != nil {
				t.Fatal(err)
			}
			invoke(true, "restore", f.Path)
		}
		assertFiles(append(files, rev.File))
		invoke(true, "fsck")
	}
	tracked := filepath.Join(root, "binary", "repeated.bin")
	if err = os.WriteFile(tracked, []byte("precious dirty bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	invoke(false, "checkout", "original")
	got, _ := os.ReadFile(tracked)
	if string(got) != "precious dirty bytes" {
		t.Fatal("dirty data lost")
	}
	invoke(true, "restore", "binary/repeated.bin")
	invoke(false, "restore", "../outside")
	invoke(false, "push", "http://127.0.0.1:1")
	invoke(false, "init")
	// An ignored untracked collision is protected too.
	if err = os.WriteFile(filepath.Join(root, ".pebbleignore"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "newfile"), []byte("tracked target"), 0644); err != nil {
		t.Fatal(err)
	}
	invoke(true, "add", ".")
	invoke(true, "commit", "new file")
	invoke(true, "branch", "with-new")
	invoke(true, "checkout", "original")
	if err = os.WriteFile(filepath.Join(root, "newfile"), []byte("precious untracked"), 0644); err != nil {
		t.Fatal(err)
	}
	invoke(false, "checkout", "with-new")
	got, _ = os.ReadFile(filepath.Join(root, "newfile"))
	if string(got) != "precious untracked" {
		t.Fatal("untracked data lost")
	}
	// A canonical but unreachable tree with a missing file revision must not
	// escape physical fsck merely because all branch histories remain valid.
	if _, err = store.CreateTree(context.Background(), []storev2.TreeEntry{
		{Path: "missing", Manifest: storev2.ID{Kind: storev2.Manifest}},
	}); err != nil {
		t.Fatal(err)
	}
	invoke(false, "fsck")
	assertFiles(append(files, revisions[0].File))

	// A second disposable repository isolates semantic corruption in a packed,
	// unreachable manifest. Public codecs keep the envelope/address canonical;
	// only reconstruction detects its false whole-file checksum.
	root = t.TempDir()
	invoke(true, "init")
	preserved := []byte("keep these exact user bytes")
	if err = os.WriteFile(filepath.Join(root, "precious"), preserved, 0644); err != nil {
		t.Fatal(err)
	}
	invoke(true, "add", ".")
	invoke(true, "commit", "preserved")
	store, err = storev2.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	head, err := store.GetRef(context.Background(), "heads/main")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.ReadCommit(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.ReadTree(context.Background(), commit.Tree)
	if err != nil {
		t.Fatal(err)
	}
	var manifest storev2.ID
	for _, entry := range entries {
		if entry.Path == "precious" {
			manifest = entry.Manifest
		}
	}
	var wire bytes.Buffer
	if err = store.Copy(context.Background(), manifest, &wire); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Clone(wire.Bytes()[storev2.HeaderSize:])
	payload[8] ^= 1 // Frozen manifest schema: u64 length, then raw SHA-256.
	bad, err := storev2.NewID(storev2.Manifest, payload)
	if err != nil {
		t.Fatal(err)
	}
	header, err := storev2.Header(storev2.Manifest, uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Put(context.Background(), bad, bytes.NewReader(append(header[:], payload...))); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pack(context.Background(), []storev2.ID{bad}); err != nil {
		t.Fatal(err)
	}
	invoke(false, "fsck")
	invoke(true, "status") // The valid branch still opens independently.
	assertFiles([]testfixture.File{{Path: "precious", Data: preserved}})
}
