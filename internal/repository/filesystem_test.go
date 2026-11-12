package repository

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func transitionFixture(t *testing.T) *Repository {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node", []byte("original file\x00"))
	committed(t, r, "file")
	if err = r.CreateBranch("file"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(r.root, "node")); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node/deep/child", []byte("nested revision\xff"))
	committed(t, r, "directory")
	if err = r.CreateBranch("directory"); err != nil {
		t.Fatal(err)
	}
	return r
}
func assertTransition(t *testing.T, r *Repository, directory bool) {
	t.Helper()
	name, want := "node", []byte("original file\x00")
	if directory {
		name, want = "node/deep/child", []byte("nested revision\xff")
	}
	got, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(name)))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("transition bytes %s: %q %v", name, got, err)
	}
	changes, err := r.Status()
	if err != nil || len(changes) != 0 {
		t.Fatalf("transition status: %v %v", changes, err)
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
}
func TestFilesystemTransitions(t *testing.T) {
	r := transitionFixture(t)
	for _, branch := range []string{"file", "directory", "file", "directory"} {
		if err := r.Checkout(branch); err != nil {
			t.Fatalf("checkout %s: %v", branch, err)
		}
		var err error
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		assertTransition(t, r, branch == "directory")
	}
}
func TestFilesystemTransitionRecovery(t *testing.T) {
	for _, branch := range []string{"file", "directory"} {
		for _, point := range []string{"journal-published", "retain-published", "retain-directory-published", "directory-published", "install-published", "head-published"} {
			if branch == "file" && point == "directory-published" || branch == "directory" && point == "retain-directory-published" {
				continue
			}
			t.Run(branch+"/"+point, func(t *testing.T) {
				r := transitionFixture(t)
				if branch == "directory" {
					if err := r.Checkout("file"); err != nil {
						t.Fatal(err)
					}
				}
				stop := errors.New("interrupted transition")
				r.fault = func(stage, path string) error {
					if stage == point {
						return stop
					}
					return nil
				}
				if err := r.Checkout(branch); !errors.Is(err, stop) {
					t.Fatalf("boundary %s: %v", point, err)
				}
				var err error
				r, err = Open(r.root)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err = r.Recover(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				assertTransition(t, r, branch == "directory")
			})
		}
	}
}
func TestFilesystemTransitionsPreserveUntrackedChildren(t *testing.T) {
	for _, emptyDirectory := range []bool{false, true} {
		t.Run(strings.ToUpper(strings.TrimSpace(map[bool]string{false: "file", true: "empty-directory"}[emptyDirectory])), func(t *testing.T) {
			r := transitionFixture(t)
			head, err := r.CurrentID()
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(r.root, "node", "untracked")
			if emptyDirectory {
				err = os.Mkdir(p, 0755)
			} else {
				err = os.WriteFile(p, []byte("precious untracked"), 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Checkout("file"); err == nil {
				t.Fatal("untracked child erased")
			}
			if got, err := r.CurrentID(); err != nil || got != head {
				t.Fatalf("HEAD changed: %s %v", got, err)
			}
			if emptyDirectory {
				if info, err := os.Lstat(p); err != nil || !info.IsDir() {
					t.Fatalf("untracked directory lost: %v", err)
				}
			} else {
				if got, err := os.ReadFile(p); err != nil || string(got) != "precious untracked" {
					t.Fatalf("untracked bytes lost: %q %v", got, err)
				}
			}
			if journalExists(t, r) {
				t.Fatal("preflight failure published intent")
			}
		})
	}
}
func TestFilesystemTransitionRecoveryPreservesLaterChildren(t *testing.T) {
	r := transitionFixture(t)
	stop := errors.New("pause")
	r.fault = func(stage, path string) error {
		if stage == "retain-published" {
			return stop
		}
		return nil
	}
	if err := r.Checkout("file"); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	write(t, r.root, "node/precious", []byte("external later data"))
	var err error
	r, err = Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Recover(context.Background()); err == nil {
		t.Fatal("later child erased")
	}
	if got, err := os.ReadFile(filepath.Join(r.root, "node", "precious")); err != nil || string(got) != "external later data" {
		t.Fatalf("later bytes lost: %q %v", got, err)
	}
	if head, err := r.GetHead(); err != nil || head != "heads/main" {
		t.Fatalf("HEAD changed: %s %v", head, err)
	}
}
func filesystemBranch(t *testing.T, r *Repository, name string, entries []storev2.TreeEntry) {
	t.Helper()
	ctx := context.Background()
	parent, err := r.CurrentID()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.store.CreateTree(ctx, entries)
	if err != nil {
		t.Fatal(err)
	}
	c := storev2.CommitData{Tree: tree, Author: "Fixture", Email: "fixture@example.invalid", Offset: "+0000", Message: name}
	if parent != (storev2.ID{}) {
		c.Parents = []storev2.ID{parent}
	}
	id, err := r.store.CreateCommit(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.UpdateRef(ctx, "heads/"+name, nil, id); err != nil {
		t.Fatal(err)
	}
}
func TestFilesystemLiteralSymlinkHistory(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious")
	if err = os.WriteFile(precious, []byte("external bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "payload", []byte("regular bytes"))
	committed(t, r, "regular")
	if err = r.CreateBranch("regular"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, target string
		directory    bool
	}{
		{"literal", "./unused/../payload", false}, {"dangling", "missing/target", false},
		{"directory", outside, true}, {"loop", "loop", false}, {"external", precious, false},
		{"metadata", ".pebble/HEAD", false},
	}
	// Native Windows fixture spelling is not a production normalization:
	// the captured API-view target itself must remain exact throughout history.
	if runtime.GOOS == "windows" {
		for i := range cases {
			cases[i].target = filepath.FromSlash(cases[i].target)
		}
	}
	for _, tc := range cases {
		p := filepath.Join(r.root, tc.name)
		if err = createLink(tc.target, p, tc.directory); err != nil {
			if os.Getenv("PEBBLE_REQUIRE_LINKS") == "1" {
				t.Fatalf("required native link capability unavailable: %v", err)
			}
			// No unsupported operation is counted as a link roundtrip. Prove the real
			// repository refuses that preserved tree before any working mutation.
			t.Logf("native link creation unavailable; testing fail-closed materialization only: %v", err)
			for _, created := range cases {
				_ = os.Remove(filepath.Join(r.root, created.name))
			}
			manifest, e := r.store.IngestFile(context.Background(), strings.NewReader(tc.target), nil)
			if e != nil {
				t.Fatal(e)
			}
			entries, e := r.TreeEntries()
			if e != nil {
				t.Fatal(e)
			}
			entries = append([]storev2.TreeEntry{{Path: "incoming", Symlink: true, DirectoryLink: tc.directory, Manifest: manifest}}, entries...)
			filesystemBranch(t, r, "unsupported-link", entries)
			if e = r.Checkout("unsupported-link"); e == nil {
				t.Fatal("unsupported host acknowledged link materialization")
			}
			if journalExists(t, r) {
				t.Fatal("unsupported materialization published intent")
			}
			if data, e := os.ReadFile(filepath.Join(r.root, "payload")); e != nil || string(data) != "regular bytes" {
				t.Fatalf("regular data changed: %q %v", data, e)
			}
			if head, e := r.GetHead(); e != nil || head != "heads/main" {
				t.Fatalf("HEAD changed: %s %v", head, e)
			}
			return
		}
	}
	first := committed(t, r, "literal links")
	if err = r.CreateBranch("links"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(r.root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err = createLink(filepath.FromSlash("../different"), filepath.Join(r.root, "dangling"), false); err != nil {
		t.Fatal(err)
	}
	committed(t, r, "changed link")
	if err = r.Checkout("links"); err != nil {
		t.Fatal(err)
	}
	r, err = Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		link, e := readLink(filepath.Join(r.root, tc.name))
		if e != nil || link.target != tc.target || measuresDirectoryLink && link.directory != tc.directory {
			t.Fatalf("literal/type lost %s: %+v %v", tc.name, link, e)
		}
		if err = os.Remove(filepath.Join(r.root, tc.name)); err != nil {
			t.Fatal(err)
		}
		write(t, r.root, tc.name, []byte("explicitly discard this file"))
		if err = r.Restore(tc.name); err != nil {
			t.Fatalf("restore link %s: %v", tc.name, err)
		}
	}
	if id, e := r.CurrentID(); e != nil || id != first {
		t.Fatalf("history changed: %s %v", id, e)
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("regular"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if _, e := os.Lstat(filepath.Join(r.root, tc.name)); !os.IsNotExist(e) {
			t.Fatalf("link removal failed: %s %v", tc.name, e)
		}
	}
	if data, e := os.ReadFile(precious); e != nil || string(data) != "external bytes" {
		t.Fatalf("external target changed: %q %v", data, e)
	}
}
func TestFilesystemExecutableMetadataHistory(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "script", []byte("executable history"))
	committed(t, r, "base")
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Executable = true
	filesystemBranch(t, r, "executable", entries)
	if !measuresExecutable {
		// Simulate opening preserved executable history on a host that cannot
		// observe the bit. A later unrelated commit must retain it verbatim.
		if err = r.store.UpdateHead(context.Background(), "heads/main", "heads/executable"); err != nil {
			t.Fatal(err)
		}
		write(t, r.root, "unrelated", []byte("new bytes"))
		committed(t, r, "preserve executable metadata")
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		entries, err = r.TreeEntries()
		if err != nil {
			t.Fatal(err)
		}
		if !entries[0].Executable {
			t.Fatal("unobservable executable identity silently cleared")
		}
		if err = r.Restore("script"); err == nil {
			t.Fatal("unsupported executable materialization acknowledged")
		}
		if journalExists(t, r) {
			t.Fatal("unsupported executable restore published intent")
		}
	} else {
		if err = r.Checkout("executable"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(r.root, "script"))
		if err != nil || info.Mode()&0111 == 0 {
			t.Fatalf("executable identity lost: %v", err)
		}
		if err = os.Chmod(filepath.Join(r.root, "script"), 0644); err != nil {
			t.Fatal(err)
		}
		committed(t, r, "nonexecutable revision")
		if err = r.Restore("script"); err != nil {
			t.Fatal(err)
		}
		info, err = os.Stat(filepath.Join(r.root, "script"))
		if err != nil || info.Mode()&0111 != 0 {
			t.Fatalf("nonexecutable revision lost: %v", err)
		}
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
}
