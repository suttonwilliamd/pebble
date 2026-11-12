package storev2

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestColdPackedRefNeverSkipsFailedPhysicalBarrier(t *testing.T) {
	for _, target := range []string{".pack", ".idx", "pack-catalog"} {
		for _, stage := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
			t.Run(target+"/"+stage, func(t *testing.T) {
				s := newStore(t)
				ctx := context.Background()
				byName, _ := corpus(t)
				putGoldens(t, s, byName, "commit-old")
				_, ids := packAll(t, s)
				removeFixtureLoose(t, s, ids)
				cold, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				injected := errors.New("cold pack barrier failure")
				cold.fault = func(point, path string) error {
					if point == stage && (filepath.Ext(path) == target || filepath.Base(path) == target) {
						return injected
					}
					return nil
				}
				tip := goldenID(t, byName["commit-old"])
				if err = cold.UpdateRef(ctx, "heads/new", nil, tip); !errors.Is(err, injected) {
					t.Fatal("physical barrier was skipped", err)
				}
				assertAbsent(t, cold.refPath("heads/new"))
				cold.fault = nil
				if err = cold.UpdateRef(ctx, "heads/new", nil, tip); err != nil {
					t.Fatal("same-handle retry", err)
				}
				reopened, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				assertRef(t, reopened, "heads/new", tip)
				for _, id := range ids {
					assertAbsent(t, s.objectPath(id))
				}
			})
		}
	}
}

func TestLooseCopiesDoNotSkipPackRefBarriers(t *testing.T) {
	for _, target := range []string{".pack", ".idx", "pack-catalog"} {
		t.Run(target, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			byName, _ := corpus(t)
			putGoldens(t, s, byName, "commit-old")
			packAll(t, s) // Keep every loose copy and the same warm Store handle.
			injected := errors.New("packed duplicate barrier failure")
			s.fault = func(stage, path string) error {
				if stage == "file-sync" && (filepath.Ext(path) == target || filepath.Base(path) == target) {
					return injected
				}
				return nil
			}
			tip := goldenID(t, byName["commit-old"])
			if err := s.UpdateRef(ctx, "heads/warm", nil, tip); !errors.Is(err, injected) {
				t.Fatal("warm loose copies skipped physical barrier", err)
			}
			assertAbsent(t, s.refPath("heads/warm"))
			s.fault = nil
			if err := s.UpdateRef(ctx, "heads/warm", nil, tip); err != nil {
				t.Fatal(err)
			}
			assertRef(t, s, "heads/warm", tip)
		})
	}
}

func TestPackCatalogRecoveryAndReadOnlyOpen(t *testing.T) {
	for _, damage := range []string{"absent", "checksum", "oversize"} {
		t.Run(damage, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			byName, _ := corpus(t)
			putGoldens(t, s, byName, "commit-old")
			_, ids := packAll(t, s)
			removeFixtureLoose(t, s, ids)
			path := s.packPath("catalog", "")
			good, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "absent" {
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				b := append([]byte(nil), good...)
				if damage == "checksum" {
					b[len(b)-1] ^= 1
				} else {
					b = make([]byte, 12+32*MaxPacks+33)
				}
				if err = os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, beforeErr := os.ReadFile(path)
			cold, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			tip := goldenID(t, byName["commit-old"])
			err = cold.Verify(ctx, tip)
			if damage == "absent" && err != nil || damage != "absent" && err == nil {
				t.Fatal("catalog read outcome", err)
			}
			after, afterErr := os.ReadFile(path)
			if !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
				t.Fatal("Open/read mutated catalog")
			}
			if err = cold.RebuildPackIndexes(ctx); err != nil {
				t.Fatal(err)
			}
			restored, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(restored, good) {
				t.Fatal("catalog not rebuilt exactly", err)
			}
			if err = cold.Verify(ctx, tip); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPackPhysicalFileBounds(t *testing.T) {
	for _, size := range []int64{0, 43, MaxPackBytes + 1} {
		s := newStore(t)
		// Truncate avoids constructing an oversized Go buffer. Filesystem
		// allocation policy differs by platform and is not asserted here.
		path := s.packPath("0000000000000000000000000000000000000000000000000000000000000000", ".pack")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(size); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		if err = s.RebuildPackIndexes(context.Background()); err == nil {
			t.Fatal("physical size accepted", size)
		}
		assertAbsent(t, s.packPath("catalog", ""))
	}
}
