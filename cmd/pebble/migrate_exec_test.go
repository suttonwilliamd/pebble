package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/repository"
	"github.com/suttonwilliamd/pebble/internal/storev2"
	v1fixture "github.com/suttonwilliamd/pebble/internal/testfixture/v1"
)

func TestExecutableMigration(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary, goName := filepath.Join(base, "pebble"), "go"
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goName += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build migration CLI: %v\n%s", err, output)
	}
	source, destination := filepath.Join(base, "source"), filepath.Join(base, "destination")
	h, err := v1fixture.Build(source, filepath.Join("..", "..", "internal", "snapshot", "testdata", "compat-v1"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := v1fixture.Fingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(directory string, success bool, args ...string) string {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Dir = directory
		output, err := command.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("CLI %v: %v\n%s", args, err, output)
		}
		if !success && strings.Contains(string(output), "Migrated and verified") {
			t.Fatal("CLI acknowledged failed migration")
		}
		return string(output)
	}
	assertSource := func() {
		t.Helper()
		after, err := v1fixture.Fingerprint(source)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("CLI mutated source state: %v", err)
		}
	}
	if output := invoke(base, true, "migrate-v1", source, destination); !strings.Contains(output, "Migrated and verified") || !strings.Contains(output, "HEAD: heads/main") {
		t.Fatal(output)
	}
	assertSource()
	data, err := os.ReadFile(filepath.Join(destination, ".pebble", "migration-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result repository.MigrationResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	s, err := storev2.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	refs, err := s.ListRefs(ctx)
	if err != nil || len(refs) != len(h.Refs) {
		t.Fatalf("CLI lost branches: %v %v", refs, err)
	}
	idFor := func(hash string) storev2.ID {
		t.Helper()
		id, err := storev2.ParseID(result.Objects[hash])
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for branch, hash := range h.Refs {
		if refs[branch] != idFor(hash) {
			t.Fatalf("CLI branch mapping lost: %s", branch)
		}
	}
	// This traverses every generated/frozen stored revision, not only HEAD and
	// not a mocked CLI forwarding function.
	for hash, original := range h.Commits {
		commit, err := s.ReadCommit(ctx, idFor(hash))
		if err != nil {
			t.Fatal(err)
		}
		if commit.Author != original.Author || commit.Email != original.Email || commit.Message != original.Message || commit.Seconds != original.Timestamp.Unix() || commit.Nanoseconds != uint32(original.Timestamp.Nanosecond()) || commit.Offset != original.Timestamp.Format("-0700") || commit.Tree != idFor(original.Tree) {
			t.Fatalf("CLI lost commit metadata %s", hash)
		}
		if original.Parent == "" {
			if len(commit.Parents) != 0 {
				t.Fatal("CLI invented parent")
			}
		} else if len(commit.Parents) != 1 || commit.Parents[0] != idFor(original.Parent) {
			t.Fatal("CLI flattened ancestry")
		}
		entries, err := s.ReadTree(ctx, commit.Tree)
		if err != nil || len(entries) != len(h.Trees[original.Tree]) {
			t.Fatalf("CLI tree mismatch: %v", err)
		}
		for i, originalEntry := range h.Trees[original.Tree] {
			entry := entries[i]
			if entry.Path != originalEntry.Name || entry.Manifest != idFor(originalEntry.Hash) {
				t.Fatal("CLI lost path/revision mapping")
			}
			var output bytes.Buffer
			if err := s.CopyFile(ctx, entry.Manifest, &output); err != nil {
				t.Fatal(err)
			}
			want := h.Blobs[originalEntry.Hash]
			if !bytes.Equal(output.Bytes(), want) || sha256.Sum256(output.Bytes()) != sha256.Sum256(want) {
				t.Fatalf("CLI historical binary mismatch %s:%s", hash, entry.Path)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(destination, "untracked.bin")); !os.IsNotExist(err) {
		t.Fatal("CLI copied uncommitted source data")
	}
	for branch, hash := range h.Refs {
		invoke(destination, true, "checkout", strings.TrimPrefix(branch, "heads/"))
		if output := invoke(destination, true, "status"); !strings.Contains(output, "Working tree clean") {
			t.Fatal(output)
		}
		for _, entry := range h.Trees[h.Commits[hash].Tree] {
			path := filepath.Join(destination, filepath.FromSlash(entry.Name))
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, h.Blobs[entry.Hash]) {
				t.Fatalf("CLI branch restoration lost %s:%s: %v", branch, entry.Name, err)
			}
		}
	}
	invoke(destination, true, "checkout", "frozen")
	if err := os.WriteFile(filepath.Join(destination, "legacy.bin"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	invoke(destination, true, "restore", "legacy.bin")
	if got, err := os.ReadFile(filepath.Join(destination, "legacy.bin")); err != nil || !bytes.Equal(got, []byte("legacy binary\x00\xff\r\n")) {
		t.Fatalf("CLI restore after migration: %q %v", got, err)
	}
	invoke(destination, true, "fsck")
	invoke(base, false, "migrate-v1", source, destination)
	invoke(base, false, "migrate-v1", source, filepath.Join(source, "inside"))
	invoke(base, false, "migrate-v1", source, filepath.Join(base, "unselected-head"), "--branch", "feature/media")
	selected := filepath.Join(base, "selected")
	invoke(base, true, "migrate-v1", source, selected, "--branch", "feature/media", "--branch", "frozen", "--head", "feature/media")
	if output := invoke(selected, true, "branch"); !strings.Contains(output, "* feature/media") || strings.Contains(output, "main") {
		t.Fatalf("CLI selection/HEAD lost: %s", output)
	}
	invoke(selected, true, "fsck")
	assertSource()
}
