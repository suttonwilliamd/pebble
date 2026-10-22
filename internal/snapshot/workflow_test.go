package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func put(t *testing.T, r *Repository, name, content string) {
	t.Helper()
	p := filepath.Join(r.RootPath(), filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func commitTest(t *testing.T, r *Repository, message string) string {
	t.Helper()
	h, err := r.Commit(message, "Test", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func content(t *testing.T, r *Repository, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.RootPath(), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestOpenAndInit(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewRepository(dir); err == nil {
		t.Fatal("opened non-repository")
	}
	if _, err := os.Stat(filepath.Join(dir, ".pebble")); !os.IsNotExist(err) {
		t.Fatal("open created metadata")
	}
	r, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	put(t, r, "sub/file", "one")
	hash := commitTest(t, r, "one")
	opened, err := NewRepository(filepath.Join(dir, "sub"))
	if err != nil || opened.RootPath() != r.RootPath() {
		t.Fatalf("ancestor discovery: %v", err)
	}
	if _, err := Init(dir); err == nil {
		t.Fatal("reinit allowed")
	}
	if got, _ := r.GetRef("heads/main"); got != hash {
		t.Fatal("reinit changed HEAD")
	}
}
func TestStatusAndCheckout(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, "a.txt", "original")
	put(t, r, "dir/delete.txt", "gone later")
	first := commitTest(t, r, "first")
	if err := r.CreateBranch("feature/topic"); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateBranch("feature/topic"); err == nil {
		t.Fatal("duplicate branch overwritten")
	}
	put(t, r, "a.txt", "edited")
	os.Remove(filepath.Join(r.RootPath(), "dir/delete.txt"))
	put(t, r, "new.txt", "new")
	changes, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0] != (Change{"M", "a.txt"}) || changes[1].Code != "D" || changes[2].Code != "A" {
		t.Fatalf("changes: %#v", changes)
	}
	if err = r.Checkout("feature/topic"); err == nil {
		t.Fatal("dirty checkout allowed")
	}
	if got, _ := r.GetRef("heads/main"); got != first {
		t.Fatal("dirty checkout changed ref")
	}
	second := commitTest(t, r, "second")
	c, err := r.GetCommit(second)
	if err != nil || c.Parent != first || c.Hash != second {
		t.Fatalf("commit chain: %+v %v", c, err)
	}
	put(t, r, "untracked.txt", "keep me")
	if err = r.Checkout("feature/topic"); err != nil {
		t.Fatal(err)
	}
	if content(t, r, "a.txt") != "original" || content(t, r, "dir/delete.txt") != "gone later" || content(t, r, "untracked.txt") != "keep me" {
		t.Fatal("incorrect restoration")
	}
	if _, err = os.Stat(filepath.Join(r.RootPath(), "new.txt")); !os.IsNotExist(err) {
		t.Fatal("target-deleted file retained")
	}
	if err = r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if content(t, r, "a.txt") != "edited" {
		t.Fatal("main not restored")
	}
	os.Remove(filepath.Join(r.RootPath(), "untracked.txt"))
	if _, err = r.Commit("no change", "Test", "e"); err == nil {
		t.Fatal("empty commit accepted")
	}
	if n, err := r.Verify(); err != nil || n < 6 {
		t.Fatalf("verify: %d %v", n, err)
	}
	refs, err := r.GetRefs()
	if err != nil || len(refs) != 2 {
		t.Fatalf("nested refs: %+v %v", refs, err)
	}
}
func TestIgnoreAndTrackedFiles(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, "tracked.log", "old")
	commitTest(t, r, "first")
	put(t, r, ".pebbleignore", "# comment\r\n\r\n*.log\r\nbuild/\r\n/root-only.txt\r\n")
	put(t, r, "nested/error.log", "ignored")
	put(t, r, "build/out.txt", "ignored")
	put(t, r, "root-only.txt", "ignored")
	put(t, r, "nested/root-only.txt", "included")
	put(t, r, "tracked.log", "new")
	commitTest(t, r, "ignored files")
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]bool{}
	for _, e := range entries {
		m[e.Name] = true
	}
	if m["nested/error.log"] || m["build/out.txt"] || m["root-only.txt"] || !m["nested/root-only.txt"] || !m["tracked.log"] {
		t.Fatalf("ignore result: %v", m)
	}
	put(t, r, ".pebbleignore", "!secret\n")
	if _, err = r.Status(); err == nil {
		t.Fatal("unsupported ignore silently accepted")
	}
}
func TestCheckoutCollisionAndCorruption(t *testing.T) {
	r := newTestRepo(t)
	put(t, r, "keep", "base")
	commitTest(t, r, "base")
	if err := r.CreateBranch("base"); err != nil {
		t.Fatal(err)
	}
	put(t, r, "new/file", "target")
	commitTest(t, r, "new file")
	if err := r.Checkout("base"); err != nil {
		t.Fatal(err)
	}
	put(t, r, "new/file", "precious untracked")
	if err := r.Checkout("main"); err == nil {
		t.Fatal("untracked overwrite allowed")
	}
	if content(t, r, "new/file") != "precious untracked" {
		t.Fatal("lost untracked data")
	}
	os.Remove(filepath.Join(r.RootPath(), "new/file"))
	head, _ := r.GetRef("heads/main")
	entries, err := r.treeForCommit(head)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	for _, e := range entries {
		if e.Name == "new/file" {
			hash = e.Hash
		}
	}
	p := filepath.Join(r.ObjectsPath(), hash[:2], hash[2:])
	if err = os.WriteFile(p, []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("main"); err == nil {
		t.Fatal("corrupt target accepted")
	}
	if content(t, r, "keep") != "base" {
		t.Fatal("preflight modified tree")
	}
	if ref, _ := r.GetHead(); ref != "heads/base" {
		t.Fatal("preflight changed HEAD")
	}
	if _, err = r.Verify(); err == nil {
		t.Fatal("fsck missed corruption")
	}
}
func TestValidationAndLock(t *testing.T) {
	r := newTestRepo(t)
	for _, ref := range []string{"../escape", "heads/../../escape", "heads/a\\b", "heads/a\n", "heads/.hidden", "heads/a.lock"} {
		if err := r.SetRef(ref, ""); err == nil {
			t.Fatalf("accepted ref %q", ref)
		}
	}
	for _, hash := range []string{"", "a", "../escape", strings.Repeat("z", 64)} {
		if _, err := r.GetObject(hash); err == nil {
			t.Fatalf("accepted hash %q", hash)
		}
		if err := r.StoreObject(&Object{Hash: hash}); err == nil {
			t.Fatalf("stored invalid object %q", hash)
		}
	}
	for _, name := range []string{"../x", ".git/config", ".PEBBLE/HEAD", "C:/escape", "a\\b", "NUL.txt", "a.", "/root"} {
		if err := validFilePath(name); err == nil {
			t.Fatalf("accepted path %q", name)
		}
	}
	unlock, err := r.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Commit("blocked", "t", "e"); err == nil {
		t.Fatal("concurrent writer allowed")
	}
	unlock()
	commitTest(t, r, "allowed")
	// A ref without a final newline must not lose its final hash character or panic.
	hash, _ := r.GetRef("heads/main")
	os.WriteFile(filepath.Join(r.refsPath, "heads/main"), []byte(hash), 0644)
	if got, err := r.GetRef("heads/main"); err != nil || got != hash {
		t.Fatalf("newline-free ref %q %v", got, err)
	}
}
func TestObjectIntegrity(t *testing.T) {
	r := newTestRepo(t)
	obj := NewObject(ObjectTypeBlob, []byte("good"))
	if err := r.StoreObject(obj); err != nil {
		t.Fatal(err)
	}
	obj.Content = []byte("evil")
	b, _ := json.Marshal(obj)
	os.WriteFile(filepath.Join(r.ObjectsPath(), obj.Hash[:2], obj.Hash[2:]), b, 0644)
	if _, err := r.GetObject(obj.Hash); err == nil {
		t.Fatal("tampered object accepted")
	}
	if err := r.StoreObject(NewObject(ObjectTypeBlob, []byte("good"))); err == nil {
		t.Fatal("existing corruption hidden")
	}
}
func TestSymlinkRefused(t *testing.T) {
	r := newTestRepo(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "precious"), []byte("safe"), 0644)
	if err := os.Symlink(outside, filepath.Join(r.RootPath(), "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := r.Commit("bad", "t", "e"); err == nil {
		t.Fatal("symlink silently omitted")
	}
	if _, err := r.safePath("link/precious"); err == nil {
		t.Fatal("symlink ancestor followed")
	}
}
