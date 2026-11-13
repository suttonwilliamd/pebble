//go:build !windows

package repository

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemPOSIXLiteralBytes(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !requireFixtureLinks(t, r.root) {
		return
	}
	committed(t, r, "empty")
	if err = r.CreateBranch("empty"); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, target string }{{"unicode", "./MiXeD/../café/é/😀"}, {"nonutf8", "literal-\xff"}, {"absolute", filepath.Join(t.TempDir(), "missing")}, {"loop", "loop"}}
	for _, tc := range cases {
		if err = createLink(tc.target, filepath.Join(r.root, tc.name), false); err != nil {
			t.Fatal(err)
		}
	}
	head := committed(t, r, "literal byte targets")
	if err = r.CreateBranch("links"); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("empty"); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("links"); err != nil {
		t.Fatal(err)
	}
	r, err = Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		p := filepath.Join(r.root, tc.name)
		target, err := os.Readlink(p)
		if err != nil || target != tc.target {
			t.Fatalf("literal bytes changed: %q want %q: %v", target, tc.target, err)
		}
		if err = os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err = r.Restore(tc.name); err != nil {
			t.Fatal(err)
		}
		target, err = os.Readlink(p)
		if err != nil || target != tc.target {
			t.Fatalf("restored target bytes changed: %q want %q: %v", target, tc.target, err)
		}
	}
	if id, err := r.CurrentID(); err != nil || id != head {
		t.Fatalf("history changed: %s %v", id, err)
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
}
func TestFilesystemPOSIXAmbiguousCapture(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !requireFixtureLinks(t, r.root) {
		return
	}
	write(t, r.root, "precious", []byte("preserved working bytes"))
	head := committed(t, r, "base")
	p := filepath.Join(r.root, "odd")
	target := `relative\literal-backslash`
	if err = createLink(target, p, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Status(); err == nil {
		t.Fatal("ambiguous native target accepted by scan")
	}
	if _, err = r.Commit("must refuse ambiguous target", "Fixture", "fixture@example.invalid"); err == nil {
		t.Fatal("ambiguous native target published")
	}
	after, err := os.Lstat(p)
	if err != nil || !os.SameFile(before, after) || after.Mode() != before.Mode() {
		t.Fatalf("native source type/identity changed: %v", err)
	}
	if got, err := os.Readlink(p); err != nil || got != target {
		t.Fatalf("native literal source changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(r.root, "precious")); err != nil || string(got) != "preserved working bytes" {
		t.Fatalf("working bytes changed: %q %v", got, err)
	}
	entries, err := os.ReadDir(r.root)
	if err != nil || len(entries) != 3 || entries[0].Name() != ".pebble" || entries[1].Name() != "odd" || entries[2].Name() != "precious" {
		t.Fatalf("public namespace changed: %v %v", entries, err)
	}
	if id, err := r.CurrentID(); err != nil || id != head {
		t.Fatalf("HEAD changed: %s %v", id, err)
	}
	if journalExists(t, r) {
		t.Fatal("capture refusal published working intent")
	}
}
