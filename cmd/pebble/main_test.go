package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

var testBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pebble-cli-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testBinary = filepath.Join(dir, "pebble.exe")
	command := exec.Command("go", "build", "-buildvcs=false", "-o", testBinary, ".")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build CLI: %v\n%s", err, output)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func runCLI(t *testing.T, dir string, success bool, args ...string) string {
	t.Helper()
	command := exec.Command(testBinary, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if (err == nil) != success {
		t.Fatalf("pebble %v: err=%v\n%s", args, err, output)
	}
	return string(output)
}

func put(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	name = filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIStatusAndCommitWorkflow(t *testing.T) {
	dir := t.TempDir()
	runCLI(t, dir, true, "init")
	put(t, dir, "asset.bin", []byte{0, 255, 1})
	put(t, dir, "remove.txt", []byte("gone"))
	put(t, dir, "debug.log", []byte("ignored"))
	out := runCLI(t, dir, true, "status")
	if !strings.Contains(out, "No commits yet") || !strings.Contains(out, `added: "asset.bin"`) || strings.Contains(out, "debug.log") {
		t.Fatal(out)
	}
	runCLI(t, dir, true, "commit", "initial snapshot")
	if out := runCLI(t, dir, true, "status"); !strings.Contains(out, "Working tree clean") {
		t.Fatal(out)
	}
	put(t, dir, "asset.bin", []byte{0, 255, 2})
	put(t, dir, "nested/new.txt", []byte("new"))
	if err := os.Remove(filepath.Join(dir, "remove.txt")); err != nil {
		t.Fatal(err)
	}
	out = runCLI(t, dir, true, "status")
	for _, want := range []string{`modified: "asset.bin"`, `added: "nested/new.txt"`, `deleted: "remove.txt"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	runCLI(t, dir, true, "commit", "updated binary and files")
	if out := runCLI(t, dir, true, "status"); !strings.Contains(out, "Working tree clean") {
		t.Fatal(out)
	}
}

func TestCLIStatusBranchUsesHeadNotIndex(t *testing.T) {
	dir := t.TempDir()
	runCLI(t, dir, true, "init")
	put(t, dir, "file.txt", []byte("main"))
	runCLI(t, dir, true, "commit", "main")
	runCLI(t, dir, true, "branch", "feature")
	runCLI(t, dir, true, "checkout", "feature")
	put(t, dir, "file.txt", []byte("feature"))
	runCLI(t, dir, true, "commit", "feature")
	if out := runCLI(t, dir, true, "status"); !strings.Contains(out, "On branch feature") || !strings.Contains(out, "Working tree clean") {
		t.Fatal(out)
	}
	runCLI(t, dir, true, "checkout", "main") // Existing checkout switches HEAD but does not restore files.
	out := runCLI(t, dir, true, "status")
	if !strings.Contains(out, "On branch main") || !strings.Contains(out, `modified: "file.txt"`) {
		t.Fatal(out)
	}
}

func TestCLIStatusUninitializedAndCorruptRepository(t *testing.T) {
	dir := t.TempDir()
	runCLI(t, dir, false, "status")
	runCLI(t, dir, false, "commit", "must not initialize")
	if _, err := os.Stat(filepath.Join(dir, ".pebble")); !os.IsNotExist(err) {
		t.Fatal("status/commit created metadata")
	}
	runCLI(t, dir, true, "init")
	put(t, dir, "file.txt", []byte("data"))
	runCLI(t, dir, true, "commit", "fixture")
	put(t, dir, ".pebble/refs/heads/main", []byte("bad\n"))
	out := runCLI(t, dir, false, "status")
	if strings.Contains(out, "Working tree clean") || strings.Contains(out, "No commits yet") {
		t.Fatal(out)
	}
	runCLI(t, dir, false, "status", "unexpected")
}

func TestCLIIgnoreRulesRetainTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	runCLI(t, dir, true, "init")
	put(t, dir, "build/tracked.bin", []byte{1})
	runCLI(t, dir, true, "commit", "tracked before ignore")
	put(t, dir, ".pebbleignore", []byte("build/\n*.log\n"))
	put(t, dir, "build/tracked.bin", []byte{2})
	put(t, dir, "build/untracked.bin", []byte{3})
	put(t, dir, "debug.log", []byte("ignored"))
	out := runCLI(t, dir, true, "status")
	if !strings.Contains(out, `modified: "build/tracked.bin"`) || strings.Contains(out, "untracked.bin") || strings.Contains(out, "debug.log") {
		t.Fatal(out)
	}
	runCLI(t, dir, true, "commit", "same rules as status")
	if out := runCLI(t, dir, true, "status"); !strings.Contains(out, "Working tree clean") {
		t.Fatal(out)
	}
}

func TestCLIInvalidIgnoreRulesFailBothCommands(t *testing.T) {
	dir := t.TempDir()
	runCLI(t, dir, true, "init")
	put(t, dir, ".pebbleignore", []byte("**/secret\n"))
	for _, args := range [][]string{{"status"}, {"commit", "invalid ignore"}} {
		out := runCLI(t, dir, false, args...)
		if !strings.Contains(out, "unsupported ignore pattern") {
			t.Fatal(out)
		}
	}
}

func TestCLICommitReportsBlobStorageFailure(t *testing.T) {
	dir := t.TempDir()
	runCLI(t, dir, true, "init")
	data := []byte("block storage")
	put(t, dir, "file.bin", data)
	hash := snapshot.ComputeHash(data)
	put(t, dir, ".pebble/objects/"+hash[:2], []byte("not a directory"))
	out := runCLI(t, dir, false, "commit", "must fail")
	if !strings.Contains(out, "Error storing blob") {
		t.Fatal(out)
	}
	ref, err := os.ReadFile(filepath.Join(dir, ".pebble", "refs", "heads", "main"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(ref)) != "" {
		t.Fatal("failed blob write advanced HEAD")
	}
}
