package repository

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// The dedicated Linux/macOS CI step requires real permission enforcement. Local
// Windows/root runs explicitly skip rather than pretending to prove POSIX access.
func TestReadOnlyOriginalCheckoutAndRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX read-only-descriptor fsync contract; Windows requires a writable handle")
	}
	unavailable := func(t *testing.T, reason string) {
		t.Helper()
		if os.Getenv("PEBBLE_REQUIRE_UNPRIVILEGED") == "1" {
			t.Fatal(reason)
		}
		t.Skip(reason)
	}
	if os.Geteuid() == 0 {
		unavailable(t, "requires an unprivileged POSIX user, not root")
	}
	for _, executable := range []bool{false, true} {
		name := "0444"
		readonly, writable := os.FileMode(0444), os.FileMode(0644)
		if executable {
			name, readonly, writable = "0555-executable", 0555, 0755
		}
		t.Run(name, func(t *testing.T) {
			r, err := Init(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.root, "tracked")
			original, target := []byte("original tracked bytes\x00\xff"), []byte("target revision\x00\x80")
			write(t, r.root, "tracked", original)
			if err = os.Chmod(path, writable); err != nil {
				t.Fatal(err)
			}
			before := committed(t, r, "original")
			if err = r.CreateBranch("original"); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "tracked", target)
			after := committed(t, r, "target")
			makeReadonly := func() os.FileInfo {
				t.Helper()
				if err := os.Chmod(path, readonly); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(path, os.O_RDWR, 0)
				if err == nil {
					if err = f.Close(); err != nil {
						t.Fatal(err)
					}
					unavailable(t, "user can write a read-only file; unprivileged permission enforcement unavailable")
				}
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("permission fixture: %v", err)
				}
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != readonly {
					t.Fatalf("fixture mode: %o want %o", info.Mode().Perm(), readonly)
				}
				return info
			}
			assertState := func(want []byte, head string, id storev2.ID) {
				t.Helper()
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("readonly working bytes: %x want %x: %v", got, want, err)
				}
				gotHead, err := r.GetHead()
				if err != nil || gotHead != head {
					t.Fatalf("readonly HEAD: %s want %s: %v", gotHead, head, err)
				}
				gotID, err := r.CurrentID()
				if err != nil || gotID != id {
					t.Fatalf("readonly revision: %s want %s: %v", gotID, id, err)
				}
				changes, err := r.Status()
				if err != nil || len(changes) != 0 {
					t.Fatalf("readonly HEAD-derived index: %v %v", changes, err)
				}
			}
			// Unchanged checkout must keep the original identity and read-only mode.
			kept := makeReadonly()
			if err = r.Checkout("main"); err != nil {
				t.Fatalf("readonly Keep checkout: %v", err)
			}
			assertState(target, "heads/main", after)
			info, err := os.Lstat(path)
			if err != nil || !os.SameFile(kept, info) || info.Mode().Perm() != readonly {
				t.Fatalf("Keep rewrote readonly original: %v", err)
			}
			// A writable parent permits replacement even when the clean original is readonly.
			if err = r.Checkout("original"); err != nil {
				t.Fatalf("readonly replacement checkout: %v", err)
			}
			assertState(original, "heads/original", before)
			makeReadonly()
			if err = r.Restore("tracked"); err != nil {
				t.Fatalf("readonly clean restore: %v", err)
			}
			assertState(original, "heads/original", before)
			// Explicit restore may discard dirty bytes without requiring file-write access.
			if err = os.Chmod(path, writable); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "tracked", []byte("explicitly discarded readonly edits\x00"))
			makeReadonly()
			if err = r.Restore("tracked"); err != nil {
				t.Fatalf("readonly dirty restore: %v", err)
			}
			assertState(original, "heads/original", before)
		})
	}
}
