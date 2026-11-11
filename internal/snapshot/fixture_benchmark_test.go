package snapshot

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func fixtureObjectBytes(b *testing.B, r *Repository) int64 {
	b.Helper()
	var size int64
	if err := filepath.WalkDir(r.ObjectsPath(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	return size
}

// One initial commit of Mixed(1 MiB), fresh disposable repository per sample.
// Init, corpus creation/materialization, verification and cleanup are excluded.
func BenchmarkFixtureSnapshotCommit(b *testing.B) {
	files, err := testfixture.Mixed(1 << 20)
	if err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	b.ReportAllocs()
	b.SetBytes(testfixture.TotalBytes(files))
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		dir, err := os.MkdirTemp(root, "commit-")
		if err != nil {
			b.Fatal(err)
		}
		r, err := Init(dir)
		if err != nil {
			b.Fatal(err)
		}
		if err := testfixture.Materialize(dir, files); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_, err = r.Commit("mixed corpus", "fixture", "fixture@example.invalid")
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := r.Verify(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(fixtureObjectBytes(b, r)), "object-B/op")
		if err := os.RemoveAll(dir); err != nil {
			b.Fatal(err)
		}
	}
}

// Alternate between original/edited versions of a 1 MiB path in a mixed repo.
// Both checkout calls are timed per operation; fixture commits are excluded.
func BenchmarkFixtureSnapshotCheckoutPair(b *testing.B) {
	files, err := testfixture.Mixed(1 << 20)
	if err != nil {
		b.Fatal(err)
	}
	r, err := Init(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	revs := testfixture.Revisions(1 << 20)
	if err := testfixture.Materialize(r.RootPath(), append(files, revs[0].File)); err != nil {
		b.Fatal(err)
	}
	if _, err := r.Commit("original", "fixture", "e"); err != nil {
		b.Fatal(err)
	}
	if err := r.CreateBranch("original"); err != nil {
		b.Fatal(err)
	}
	if err := testfixture.Materialize(r.RootPath(), []testfixture.File{revs[2].File}); err != nil {
		b.Fatal(err)
	}
	if _, err := r.Commit("edited", "fixture", "e"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(2 * (testfixture.TotalBytes(files) + int64(len(revs[0].File.Data))))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Checkout("original"); err != nil {
			b.Fatal(err)
		}
		if err := r.Checkout("main"); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	got, err := os.ReadFile(filepath.Join(r.RootPath(), filepath.FromSlash(revs[2].File.Path)))
	if err != nil || !bytes.Equal(got, revs[2].File.Data) {
		b.Fatalf("checkout bytes differ: %v", err)
	}
	b.ReportMetric(float64(fixtureObjectBytes(b, r)), "object-B/op")
}
