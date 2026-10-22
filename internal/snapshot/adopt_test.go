package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckoutAdoptsIdenticalUntrackedFileWithoutRewriting(t *testing.T) {
	r := newTestRepo(t)
	commitTest(t, r, "empty")
	if err := r.CreateBranch("empty"); err != nil {
		t.Fatal(err)
	}
	put(t, r, "shared", "identical")
	commitTest(t, r, "with file")
	if err := r.Checkout("empty"); err != nil {
		t.Fatal(err)
	}
	put(t, r, "shared", "identical")
	p := filepath.Join(r.RootPath(), "shared")
	stamp := time.Unix(1000000, 0)
	if err := os.Chtimes(p, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("identical untracked file was rewritten")
	}
	changes, err := r.Status()
	if err != nil || len(changes) != 0 {
		t.Fatalf("adopted file not tracked: %v %v", changes, err)
	}
}
