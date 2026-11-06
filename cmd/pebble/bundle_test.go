package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

func TestBundleCLITransferAndPreservation(t *testing.T) {
	t.Setenv("PEBBLE_AUTHOR", "Bundle CLI Author")
	t.Setenv("PEBBLE_EMAIL", "bundle-cli@example.test")
	var out bytes.Buffer
	call := func(cwd string, args ...string) string {
		t.Helper()
		out.Reset()
		if err := run(args, cwd, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	source, dest := t.TempDir(), t.TempDir()
	call(source, "init")
	asset := filepath.Join(source, "asset.bin")
	if err := os.WriteFile(asset, []byte{0, 255, 42}, 0644); err != nil {
		t.Fatal(err)
	}
	call(source, "commit", "first binary")
	if err := os.WriteFile(asset, []byte{255, 0, 128, 0}, 0644); err != nil {
		t.Fatal(err)
	}
	call(source, "commit", "second binary")
	wantHistory := call(source, "log")
	sub := filepath.Join(source, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if s := call(sub, "bundle", "create", "history.bundle"); !strings.Contains(s, "Created verified HEAD history bundle") {
		t.Fatal(s)
	}
	path := filepath.Join(sub, "history.bundle")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"bundle", "create", "history.bundle"}, sub, &out); err == nil {
		t.Fatal("CLI overwrote bundle")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, data) {
		t.Fatal("bundle bytes changed")
	}
	transferred := filepath.Join(t.TempDir(), "transferred.bundle")
	if err = os.WriteFile(transferred, data, 0600); err != nil {
		t.Fatal(err)
	}
	call(dest, "init")
	local := filepath.Join(dest, "local.txt")
	if err = os.WriteFile(local, []byte("local history"), 0644); err != nil {
		t.Fatal(err)
	}
	call(dest, "commit", "local destination")
	if err = os.WriteFile(local, []byte("dirty work"), 0644); err != nil {
		t.Fatal(err)
	}
	headPath := filepath.Join(dest, ".pebble", "HEAD")
	beforeHead, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLog := call(dest, "log")
	if s := call(dest, "bundle", "import", transferred, "recovered/main"); !strings.Contains(s, "HEAD and working files unchanged") {
		t.Fatal(s)
	}
	if afterHead, _ := os.ReadFile(headPath); !bytes.Equal(afterHead, beforeHead) {
		t.Fatal("CLI import changed HEAD")
	}
	if after, _ := os.ReadFile(local); string(after) != "dirty work" {
		t.Fatal("CLI import changed dirty work")
	}
	if call(dest, "log") != beforeLog {
		t.Fatal("CLI import changed current branch")
	}
	for _, branch := range []string{"main", "recovered/main"} {
		if err = run([]string{"bundle", "import", transferred, branch}, dest, &out); err == nil {
			t.Fatalf("CLI overwrote branch %s", branch)
		}
	}
	call(dest, "restore", "local.txt")
	call(dest, "checkout", "recovered/main")
	if got := call(dest, "log"); got != wantHistory {
		t.Fatalf("history changed:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "asset.bin")); !bytes.Equal(got, []byte{255, 0, 128, 0}) {
		t.Fatal("CLI binary transfer failed")
	}
	if s := call(dest, "fsck"); !strings.Contains(s, "reachable objects") {
		t.Fatal(s)
	}
}

func TestBundleCLIHelpAndErrors(t *testing.T) {
	outside := t.TempDir()
	for _, args := range [][]string{{"help"}, {"bundle", "help"}, {"bundle", "--help"}, {"bundle", "-h"}} {
		var out bytes.Buffer
		if err := run(args, outside, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, want := range []string{"bundle create <file>", "bundle import <file> <new-branch>"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("help omitted %q: %s", want, out.String())
			}
		}
	}
	var out bytes.Buffer
	if err := run([]string{"bundle", "import", "missing", "recovered"}, outside, &out); err == nil || !strings.Contains(err.Error(), "not a Pebble repository") {
		t.Fatalf("import silently initialized repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, ".pebble")); !os.IsNotExist(err) {
		t.Fatal("import created metadata")
	}
	root := t.TempDir()
	if err := run([]string{"init"}, root, &out); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"bundle"}, {"bundle", "create"}, {"bundle", "create", "a", "extra"},
		{"bundle", "import"}, {"bundle", "import", "a"}, {"bundle", "import", "a", "b", "extra"},
		{"bundle", "unknown", "a"},
	} {
		if err := run(args, root, &out); err == nil || !strings.Contains(err.Error(), "usage: pebble bundle") {
			t.Errorf("%v: %v", args, err)
		}
	}
	path := filepath.Join(t.TempDir(), "unborn.bundle")
	if err := run([]string{"bundle", "create", path}, root, &out); err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("empty history: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("empty export created a file")
	}
	if err := run([]string{"bundle", "import", "missing.bundle", "recovered"}, root, &out); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestBundleInspectOutsideRepository(t *testing.T) {
	source, outside := t.TempDir(), t.TempDir()
	repo, err := snapshot.Init(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "asset.bin")
	if err = os.WriteFile(path, []byte{0, 255, 1}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Commit("first", "First Author", "first@example.test"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte{0, 254, 2}, 0644); err != nil {
		t.Fatal(err)
	}
	tip, err := repo.Commit("second", "Second Author", "second@example.test")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(outside, "history.bundle")
	if err = repo.CreateBundleFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = run([]string{"bundle", "inspect", "history.bundle"}, outside, &out); err != nil {
		t.Fatal(err)
	}
	var info snapshot.BundleInfo
	if err = json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("summary is not JSON: %v", err)
	}
	want := snapshot.BundleInfo{Version: 1, SourceRef: "heads/main", Tip: tip,
		ObjectCount: 6, CommitCount: 2, TreeCount: 2, BlobCount: 2}
	if info != want {
		t.Fatalf("summary: %+v; want %+v", info, want)
	}
	for _, args := range [][]string{
		{"bundle", "inspect"}, {"bundle", "inspect", "history.bundle", "extra"},
		{"bundle", "inspect", "missing.bundle"},
	} {
		out.Reset()
		if err = run(args, outside, &out); err == nil || out.Len() != 0 {
			t.Fatalf("%v reported unverified summary: %q, %v", args, out.String(), err)
		}
	}
	if err = os.WriteFile(path, data[:len(data)-1], 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = run([]string{"bundle", "inspect", "history.bundle"}, outside, &out); err == nil || out.Len() != 0 {
		t.Fatalf("truncated input reported success: %q, %v", out.String(), err)
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 1 || files[0].Name() != "history.bundle" {
		t.Fatalf("inspection created files/metadata: %v, %v", files, err)
	}
}
