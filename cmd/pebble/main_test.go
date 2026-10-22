package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDogfoodCLI(t *testing.T) {
	t.Setenv("PEBBLE_AUTHOR", "Dogfood User")
	t.Setenv("PEBBLE_EMAIL", "dogfood@example.com")
	root := t.TempDir()
	var out bytes.Buffer
	call := func(args ...string) string {
		t.Helper()
		out.Reset()
		if err := run(args, root, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	call("init")
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0644)
	if s := call("status"); !strings.Contains(s, "A  hello.txt") {
		t.Fatal(s)
	}
	call("commit", "-m", "initial commit")
	call("branch", "topic")
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("world"), 0644)
	call("commit", "second", "commit")
	s := call("log")
	if !strings.Contains(s, "initial commit") || !strings.Contains(s, "second commit") || strings.Count(s, "Author: Dogfood User") != 2 {
		t.Fatal(s)
	}
	call("checkout", "topic")
	b, _ := os.ReadFile(filepath.Join(root, "hello.txt"))
	if string(b) != "hello" {
		t.Fatal("checkout did not restore")
	}
	if s = call("status"); !strings.Contains(s, "On branch topic") || !strings.Contains(s, "Working tree clean") {
		t.Fatal(s)
	}
	call("fsck")
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("discard"), 0644)
	call("restore", "hello.txt")
	if b, _ := os.ReadFile(filepath.Join(root, "hello.txt")); string(b) != "hello" {
		t.Fatal("CLI restore failed")
	}
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0755)
	out.Reset()
	if err := run([]string{"status"}, sub, &out); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("discard again"), 0644)
	if err := run([]string{"restore", "../hello.txt"}, sub, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "hello.txt")); string(b) != "hello" {
		t.Fatal("subdirectory restore failed")
	}
}
func TestCLIErrors(t *testing.T) {
	t.Setenv("PEBBLE_AUTHOR", "")
	t.Setenv("PEBBLE_EMAIL", "")
	root := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"init"}, root, &out); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{}, {"unknown"}, {"commit", "message"}, {"status", "extra"}, {"log", "extra"}, {"checkout"}, {"branch", "a", "b"}, {"fsck", "extra"}, {"push", "https://example.com"}, {"pull", "https://example.com"}} {
		if err := run(args, root, &out); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}
