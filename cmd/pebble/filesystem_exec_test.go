package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestFilesystemCLI(t *testing.T) {
	suffix, goName := "", "go"
	if runtime.GOOS == "windows" {
		suffix, goName = ".exe", "go.exe"
	}
	bin := filepath.Join(t.TempDir(), "pebble"+suffix)
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v\n%s", err, out)
	}
	root := t.TempDir()
	invoke := func(ok bool, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PEBBLE_AUTHOR=Filesystem CLI Fixture", "PEBBLE_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("actual CLI %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	write := func(name, data string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	assertFile := func(name, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatalf("CLI working bytes %s: %q %v", name, got, err)
		}
	}
	write("node", "original CLI file\x00")
	invoke(true, "init")
	invoke(true, "commit", "file baseline")
	invoke(true, "branch", "node-file")
	if err := os.Remove(filepath.Join(root, "node")); err != nil {
		t.Fatal(err)
	}
	write("node/deep/child", "nested CLI bytes\x00\xff")
	invoke(true, "commit", "directory revision")
	invoke(true, "branch", "node-directory")
	invoke(true, "checkout", "node-file")
	assertFile("node", "original CLI file\x00")
	write("node", "precious dirty CLI bytes")
	invoke(false, "checkout", "node-directory")
	assertFile("node", "precious dirty CLI bytes")
	invoke(true, "restore", "node")
	assertFile("node", "original CLI file\x00")
	invoke(true, "recover")
	invoke(true, "checkout", "node-directory")
	assertFile("node/deep/child", "nested CLI bytes\x00\xff")
	write("node/deep/untracked", "untracked CLI data")
	invoke(false, "checkout", "node-file")
	assertFile("node/deep/untracked", "untracked CLI data")
	if err := os.Remove(filepath.Join(root, "node", "deep", "untracked")); err != nil {
		t.Fatal(err)
	}
	invoke(true, "checkout", "node-file")
	assertFile("node", "original CLI file\x00")
	if err := os.Remove(filepath.Join(root, "node")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "node"), 0755); err != nil {
		t.Fatal(err)
	}
	invoke(true, "restore", "node")
	assertFile("node", "original CLI file\x00")
	literal := "./missing/../target"
	linkPath := filepath.Join(root, "literal")
	if err := os.Symlink(literal, linkPath); err != nil {
		if os.Getenv("PEBBLE_REQUIRE_LINKS") == "1" {
			t.Fatalf("required native CLI link capability unavailable: %v", err)
		}
		t.Logf("native link creation unavailable; proving actual CLI fail-closed materialization, not a link roundtrip: %v", err)
		store, e := storev2.Open(root)
		if e != nil {
			t.Fatal(e)
		}
		ctx := context.Background()
		parent, e := store.GetRef(ctx, "heads/node-file")
		if e != nil {
			t.Fatal(e)
		}
		c, e := store.ReadCommit(ctx, parent)
		if e != nil {
			t.Fatal(e)
		}
		entries, e := store.ReadTree(ctx, c.Tree)
		if e != nil {
			t.Fatal(e)
		}
		manifest, e := store.IngestFile(ctx, strings.NewReader(literal), nil)
		if e != nil {
			t.Fatal(e)
		}
		entries = append(entries, storev2.TreeEntry{Path: "literal", Symlink: true, Manifest: manifest})
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
		tree, e := store.CreateTree(ctx, entries)
		if e != nil {
			t.Fatal(e)
		}
		revision, e := store.CreateCommit(ctx, storev2.CommitData{Tree: tree, Parents: []storev2.ID{parent}, Author: "Fixture", Email: "fixture@example.invalid", Offset: "+0000", Message: "preserved link history"})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.UpdateRef(ctx, "heads/unavailable-link", nil, revision); e != nil {
			t.Fatal(e)
		}
		invoke(false, "checkout", "unavailable-link")
		assertFile("node", "original CLI file\x00")
		if head, e := store.Head(ctx); e != nil || head != "heads/node-file" {
			t.Fatalf("unsupported CLI checkout changed HEAD: %s %v", head, e)
		}
		if _, e = os.Lstat(filepath.Join(root, ".pebble", "worktree-journal")); !os.IsNotExist(e) {
			t.Fatalf("unsupported CLI checkout published intent: %v", e)
		}
	} else {
		invoke(true, "commit", "literal link")
		invoke(true, "branch", "literal-state")
		if err = os.Remove(linkPath); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink("literal", linkPath); err != nil {
			t.Fatal(err)
		}
		invoke(true, "commit", "looping link")
		invoke(true, "checkout", "literal-state")
		if target, e := os.Readlink(linkPath); e != nil || target != literal {
			t.Fatalf("CLI literal target changed: %q %v", target, e)
		}
		if err = os.Remove(linkPath); err != nil {
			t.Fatal(err)
		}
		write("literal", "discard regular replacement")
		invoke(true, "restore", "literal")
		if target, e := os.Readlink(linkPath); e != nil || target != literal {
			t.Fatalf("CLI restore flattened link: %q %v", target, e)
		}
		invoke(true, "checkout", "node-directory")
		assertFile("node/deep/child", "nested CLI bytes\x00\xff")
	}
	invoke(true, "recover")
	invoke(true, "fsck")
	if out := invoke(true, "log"); !strings.Contains(out, "file baseline") {
		t.Fatal("real CLI history lost file ancestor")
	}
	if out := invoke(true, "branch"); !strings.Contains(out, "node-directory") {
		t.Fatal("real CLI lost directory history branch")
	}
	if out := invoke(true, "status"); !strings.Contains(out, "Working tree clean") {
		t.Fatalf("real CLI not coherent: %s", out)
	}
}
