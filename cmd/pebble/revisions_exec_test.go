package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestRevisionInspectionCLI(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "pebble"+suffix)
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"+suffix), "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v\n%s", err, out)
	}
	root := t.TempDir()
	env := append(isolatedIdentityEnv(t), "PEBBLE_AUTHOR=Revision Fixture", "PEBBLE_EMAIL=revision@example.invalid")
	invoke := func(ok bool, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	put := func(name string, data []byte) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	assertBytes := func(name string, want []byte) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s got %x want %x: %v", name, got, want, err)
		}
	}
	invoke(true, "init")
	oldText := []byte("same\nduplicate\ntail\nduplicate\n")
	newText := []byte("duplicate\nsame\nduplicate\ntail")
	oldBinary := make([]byte, 65540)
	for i := range oldBinary {
		oldBinary[i] = byte(i % 251)
	}
	newBinary := append([]byte(nil), oldBinary...)
	newBinary[0] ^= 255
	newBinary[1] ^= 255
	for i := 32767; i < 32771; i++ {
		newBinary[i] ^= 128
	}
	newBinary[65535] ^= 1
	newBinary = append(newBinary, 0, 255, 3)
	put("text", oldText)
	put("binary", oldBinary)
	put("gone", []byte("first only"))
	put("untouched", []byte("stable"))
	put("node/child", []byte("old child"))
	invoke(true, "add", ".")
	invoke(true, "commit", "first revision")
	invoke(true, "branch", "first")
	store, err := storev2.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.GetRef(context.Background(), "heads/main")
	if err != nil {
		t.Fatal(err)
	}
	put("text", newText)
	put("binary", newBinary)
	if err = os.Remove(filepath.Join(root, "gone")); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(root, "node")); err != nil {
		t.Fatal(err)
	}
	put("node", []byte("new leaf"))
	put("new-only", []byte("second only"))
	invoke(true, "add", ".")
	invoke(true, "commit", "second revision")
	if got := invoke(true, "show", "HEAD~1", "--", "binary"); !bytes.Equal(got, oldBinary) {
		t.Fatal("show decoded or changed literal binary bytes")
	}
	if got := invoke(true, "show", first.String(), "--", "text"); !bytes.Equal(got, oldText) {
		t.Fatal("show full typed non-HEAD text")
	}
	log := string(invoke(true, "log", "HEAD^"))
	if strings.Count(log, "commit pv2:") != 1 || !strings.Contains(log, "first revision") || strings.Contains(log, "second revision") {
		t.Fatal(log)
	}
	patch := string(invoke(true, "diff", "first", "HEAD", "--", "text"))
	want := "@@ -1,4 +1,4 @@\n-same\n-duplicate\n-tail\n-duplicate\n+duplicate\n+same\n+duplicate\n+tail\n\\ No newline at end of file\n"
	if !strings.HasSuffix(patch, want) {
		t.Fatalf("ordered duplicate/newline patch:\n%s", patch)
	}
	summary := string(invoke(true, "diff", first.String(), "HEAD", "--", "binary"))
	for _, want := range []string{
		fmt.Sprintf("old binary size=%d sha256=%x\n", len(oldBinary), sha256.Sum256(oldBinary)),
		fmt.Sprintf("new binary size=%d sha256=%x\n", len(newBinary), sha256.Sum256(newBinary)),
		"region old offset=0 length=2 new offset=0 length=2\n",
		"region old offset=32767 length=4 new offset=32767 length=4\n",
		"region old offset=65535 length=1 new offset=65535 length=1\n",
		"region old offset=65540 length=0 new offset=65540 length=3\n",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("missing %q:\n%s", want, summary)
		}
	}
	// A one-byte prefix insertion in repetitive binary content is deliberately
	// compared at equal offsets, not reported as a one-byte aligned edit or as
	// chunk-boundary churn. Assert the complete summary, including the tail.
	put("shift", []byte{0, 1, 0, 1, 0, 1})
	invoke(true, "add", "shift")
	shiftOld := []byte{0, 1, 0, 1, 0, 1}
	shiftNew := []byte{255, 0, 1, 0, 1, 0, 1}
	put("shift", shiftNew)
	shiftSummary := string(invoke(true, "diff", "--", "shift"))
	shiftWant := fmt.Sprintf("diff --pebble %q\nold mode regular\nnew mode regular\nold binary size=6 sha256=%x\nnew binary size=7 sha256=%x\nregion old offset=0 length=6 new offset=0 length=6\nregion old offset=6 length=0 new offset=6 length=1\n", "shift", sha256.Sum256(shiftOld), sha256.Sum256(shiftNew))
	if shiftSummary != shiftWant {
		t.Fatalf("repetitive insertion equal-offset summary:\n%s\nwant:\n%s", shiftSummary, shiftWant)
	}
	show := string(invoke(true, "show", "HEAD"))
	if !strings.Contains(show, "second revision") || !strings.Contains(show, "region old offset=32767 length=4") {
		t.Fatal(show)
	}
	put("text", []byte("staged\n"))
	invoke(true, "add", "text")
	put("text", []byte("working\n"))
	put("precious", []byte("untracked"))
	index, err := os.ReadFile(filepath.Join(root, ".pebble", "index"))
	if err != nil {
		t.Fatal(err)
	}
	head, err := store.GetRef(context.Background(), "heads/main")
	if err != nil {
		t.Fatal(err)
	}
	staged := string(invoke(true, "diff", "--staged", "--", "text"))
	unstaged := string(invoke(true, "diff", "--", "text"))
	if !strings.Contains(staged, "+staged\n") || strings.Contains(staged, "working") || !strings.Contains(unstaged, "-staged\n+working\n") {
		t.Fatalf("staging sides:\n%s\n%s", staged, unstaged)
	}
	historical := string(invoke(true, "diff", "first", "--", "text"))
	if !strings.Contains(historical, "-same\n") || !strings.Contains(historical, "+working\n") {
		t.Fatal(historical)
	}
	for _, args := range [][]string{{"show", "missing"}, {"log", "HEAD~99"}, {"diff", "text"}, {"show", "pv2:tree:sha256:" + strings.Repeat("0", 64)}, {"show", "pv2:commit:sha256:" + strings.Repeat("0", 64)}, {"show", "HEAD", "--", "missing"}, {"diff", "HEAD", "first", "--", "missing"}, {"restore", "--source", "missing", "text"}, {"restore", "--source", "first", "../outside"}, {"restore", "--source", "first", "absent"}} {
		invoke(false, args...)
	}
	assertBytes("text", []byte("working\n"))
	invoke(true, "restore", "--source", "first", "--", "text")
	assertBytes("text", oldText)
	invoke(true, "restore", "--source", first.String(), "binary")
	assertBytes("binary", oldBinary)
	invoke(true, "restore", "--source", "first", "node")
	assertBytes("node/child", []byte("old child"))
	invoke(false, "restore", "--source", "HEAD", "node")
	assertBytes("node/child", []byte("old child"))
	if err = os.Remove(filepath.Join(root, "node", "child")); err != nil {
		t.Fatal(err)
	}
	invoke(true, "restore", "--source", "HEAD", "node")
	assertBytes("node", []byte("new leaf"))
	invoke(true, "restore", "--source", "first", "new-only")
	if _, err = os.Lstat(filepath.Join(root, "new-only")); !os.IsNotExist(err) {
		t.Fatalf("missing source did not delete tracked target: %v", err)
	}
	put("gone", []byte("untracked collision"))
	invoke(false, "restore", "--source", "first", "gone")
	assertBytes("gone", []byte("untracked collision"))
	if err = os.Remove(filepath.Join(root, "gone")); err != nil {
		t.Fatal(err)
	}
	invoke(true, "restore", "--source", "first", "gone")
	assertBytes("gone", []byte("first only"))
	invoke(true, "restore", "text")
	assertBytes("text", newText)
	assertBytes("untouched", []byte("stable"))
	assertBytes("precious", []byte("untracked"))
	gotIndex, err := os.ReadFile(filepath.Join(root, ".pebble", "index"))
	if err != nil || !bytes.Equal(index, gotIndex) {
		t.Fatal("restore/inspection changed staged revisions", err)
	}
	reopened, err := storev2.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	gotHead, err := reopened.GetRef(context.Background(), "heads/main")
	if err != nil || gotHead != head {
		t.Fatal("restore moved history", err)
	}
	status := string(invoke(true, "status"))
	if !strings.Contains(status, "MM text") || !strings.Contains(status, "?? precious") {
		t.Fatal(status)
	}
	invoke(true, "fsck")
}
