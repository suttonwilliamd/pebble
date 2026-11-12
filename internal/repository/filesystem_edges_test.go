package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestFilesystemDirectoryHintHistory(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !requireFixtureLinks(t, r.root) {
		return
	}
	write(t, r.root, "precious", []byte("base"))
	committed(t, r, "base")
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	target := "literal-dangling-directory"
	manifest, err := r.store.IngestFile(context.Background(), strings.NewReader(target), nil)
	if err != nil {
		t.Fatal(err)
	}
	entries = append([]storev2.TreeEntry{{Path: "hint", Symlink: true, DirectoryLink: true, Manifest: manifest}}, entries...)
	filesystemBranch(t, r, "hint", entries)
	if err = r.Checkout("hint"); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "unrelated", []byte("new revision"))
	committed(t, r, "preserve directory hint")
	r, err = Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err = r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	if !entries[0].Symlink || !entries[0].DirectoryLink || entries[0].Manifest != manifest {
		t.Fatalf("unobservable directory-link identity lost: %+v", entries[0])
	}
	if err = r.Restore("hint"); err != nil {
		t.Fatal(err)
	}
	link, err := readLink(filepath.Join(r.root, "hint"))
	if err != nil || link.target != target || measuresDirectoryLink && !link.directory {
		t.Fatalf("directory hint target/type changed: %+v %v", link, err)
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
}
func TestFilesystemEmptyDirectoryTransitionAndRestore(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	committed(t, r, "empty history")
	if err = r.CreateBranch("empty"); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node", []byte("stored leaf"))
	committed(t, r, "file revision")
	if err = r.CreateBranch("file"); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("empty"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(r.root, "node"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(r.root, "node"), 0755) })
	if err = r.Checkout("file"); err != nil {
		t.Fatalf("safe empty directory to file: %v", err)
	}
	if err = os.Remove(filepath.Join(r.root, "node")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(r.root, "node"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = r.Restore("node"); err != nil {
		t.Fatalf("explicit leaf restore over empty directory: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(r.root, "node")); err != nil || string(got) != "stored leaf" {
		t.Fatalf("stored leaf lost: %q %v", got, err)
	}
}
func TestFilesystemTransitionCLIRecovery(t *testing.T) {
	suffix, goName := "", "go"
	if runtime.GOOS == "windows" {
		suffix, goName = ".exe", "go.exe"
	}
	bin := filepath.Join(t.TempDir(), "pebble"+suffix)
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", bin, "../../cmd/pebble")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual recovery CLI: %v\n%s", err, out)
	}
	r := transitionFixture(t)
	for _, step := range []struct{ branch, point string }{{"file", "retain-directory-published"}, {"directory", "directory-published"}} {
		interruptWorktree(t, r.root, "PEBBLE_TEST_WORKTREE_BRANCH="+step.branch, "PEBBLE_TEST_WORKTREE_STAGE="+step.point)
		for i := 0; i < 2; i++ {
			cmd := exec.Command(bin, "recover")
			cmd.Dir = r.root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("actual CLI transition recovery: %v\n%s", err, out)
			}
		}
		var err error
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		assertTransition(t, r, step.branch == "directory")
		cmd := exec.Command(bin, "fsck")
		cmd.Dir = r.root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("actual CLI fsck after process interruption: %v\n%s", err, out)
		}
	}
}
func TestFilesystemLinkStageCancellationAndMissingSource(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := r.store.IngestFile(context.Background(), strings.NewReader("literal-target"), nil)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.stageFileContext(ctx, stage, storev2.TreeEntry{Path: "link", Symlink: true, Manifest: manifest}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled link materialization did not preserve cancellation: %v", err)
	}
	if _, err = r.stageFile(stage, storev2.TreeEntry{Path: "link", Symlink: true, Manifest: storev2.ID{Kind: storev2.Manifest}}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source was not reported before materialization: %v", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed link materialization left provisional objects: %v %v", entries, err)
	}
}
