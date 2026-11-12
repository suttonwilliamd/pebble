package repository

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestFilesystemUnsupportedTargetInterpretation(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "precious", []byte("unchanged working bytes"))
	committed(t, r, "base")
	cases := []string{`C:\generated`, `\\server\share`, `relative\backslash`, `C:stateful`}
	if runtime.GOOS == "windows" {
		cases = []string{"/generated/absolute", "relative/slash", `C:stateful`, `\root-relative`, `file:stream`, "invalid-\xff"}
	}
	head, err := r.CurrentID()
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			manifest, err := r.store.IngestFile(context.Background(), strings.NewReader(target), nil)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := r.TreeEntries()
			if err != nil {
				t.Fatal(err)
			}
			entries = append([]storev2.TreeEntry{{Path: "incoming", Symlink: true, Manifest: manifest}}, entries...)
			branch := fmt.Sprintf("unsupported-%d", i)
			filesystemBranch(t, r, branch, entries)
			before := publicSnapshot(t, r.root)
			if err = r.Checkout(branch); err == nil {
				t.Fatal("ambiguous host interpretation acknowledged")
			}
			assertPublicSnapshot(t, r.root, before)
			if id, err := r.CurrentID(); err != nil || id != head {
				t.Fatalf("HEAD changed: %s %v", id, err)
			}
			if journalExists(t, r) {
				t.Fatal("unsupported target published recovery intent")
			}
			if _, err = r.Verify(); err != nil {
				t.Fatalf("preserved unsupported history became unreadable: %v", err)
			}
		})
	}
}
