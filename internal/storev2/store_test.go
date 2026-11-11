package storev2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	pe "github.com/suttonwilliamd/pebble/internal/errors"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func TestRefCASAncestryAndTags(t *testing.T) {
	byName, _ := corpus(t)
	s := newStore(t)
	putGoldens(t, s, byName, "commit-merge", "tag-annotated", "tag-nested", "tag-tree", "manifest-new")
	id := func(name string) ID { return goldenID(t, byName[name]) }
	ctx := context.Background()
	old, next, side, merge := id("commit-old"), id("commit-new"), id("commit-side"), id("commit-merge")
	if err := s.UpdateRef(ctx, "heads/main", nil, old); err != nil {
		t.Fatal(err)
	}
	wrong := id("commit-root")
	for _, expected := range []*ID{nil, &wrong} {
		if err := s.UpdateRef(ctx, "heads/main", expected, next); !errors.Is(err, ErrConflict) {
			t.Fatal("stale CAS accepted:", err)
		}
		assertRef(t, s, "heads/main", old)
	}
	if err := s.UpdateRef(ctx, "heads/main", &old, next); err != nil {
		t.Fatal(err)
	}
	// A retry after a lost acknowledgement must verify before succeeding.
	if err := s.UpdateRef(ctx, "heads/main", &old, next); err != nil {
		t.Fatal("identical retry:", err)
	}
	for _, target := range []ID{old, side} {
		if err := s.UpdateRef(ctx, "heads/main", &next, target); !errors.Is(err, ErrConflict) {
			t.Fatal("rewind/divergence accepted:", err)
		}
		assertRef(t, s, "heads/main", next)
	}
	if err := s.UpdateRef(ctx, "heads/second-parent", nil, side); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef(ctx, "heads/second-parent", &side, merge); err != nil {
		t.Fatal("second-parent fast-forward rejected:", err)
	}
	assertRef(t, s, "heads/second-parent", merge)
	if err := s.UpdateRef(ctx, "heads/not-commit", nil, id("tag-annotated")); err == nil {
		t.Fatal("branch accepted tag target")
	}
	for _, collision := range []string{"heads/Main", "heads/main/child"} {
		if err := s.UpdateRef(ctx, collision, nil, next); !errors.Is(err, ErrConflict) {
			t.Fatal("colliding ref accepted:", err)
		}
	}
	if err := s.UpdateRef(ctx, "heads/parent/child", nil, old); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef(ctx, "heads/parent", nil, old); !errors.Is(err, ErrConflict) {
		t.Fatal("file/descendant collision accepted:", err)
	}
	annotation := id("tag-annotated")
	if err := s.UpdateRef(ctx, "tags/v2.0", nil, annotation); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef(ctx, "tags/v2.0", nil, annotation); err != nil {
		t.Fatal("tag retry:", err)
	}
	for _, expected := range []*ID{nil, &annotation} {
		if err := s.UpdateRef(ctx, "tags/v2.0", expected, next); !errors.Is(err, ErrConflict) {
			t.Fatal("tag moved:", err)
		}
	}
	if err := s.UpdateRef(ctx, "tags/wrong-name", nil, annotation); err == nil {
		t.Fatal("annotation renamed implicitly")
	}
	if err := s.UpdateRef(ctx, "tags/tree", nil, id("tree-empty")); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef(ctx, "tags/file", nil, id("manifest-new")); err != nil {
		t.Fatal(err)
	}
	// Separate namespaces are legal, despite equal suffixes.
	if err := s.UpdateRef(ctx, "tags/main", nil, next); err != nil {
		t.Fatal(err)
	}
	assertRef(t, s, "tags/v2.0", annotation)
	assertRef(t, s, "tags/tree", id("tree-empty"))
	assertRef(t, s, "tags/file", id("manifest-new"))
}

func TestExistingCorruptionNeverOverwrittenOrAcknowledged(t *testing.T) {
	byName, _ := corpus(t)
	good := goldenBytes(t, byName["chunk-a"])
	cases := map[string]func([]byte) []byte{
		"truncated": func(b []byte) []byte { return b[:len(b)-1] },
		"trailing":  func(b []byte) []byte { return append(b, 0) },
		"kind":      func(b []byte) []byte { b[4] = byte(Tree); return b },
		"size":      func(b []byte) []byte { b[12] = 2; return b },
		"content":   func(b []byte) []byte { b[len(b)-1] = 'B'; return b },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			putGoldens(t, s, byName, "commit-old")
			ctx := context.Background()
			commit, chunk := goldenID(t, byName["commit-old"]), goldenID(t, byName["chunk-a"])
			if err := s.UpdateRef(ctx, "heads/main", nil, commit); err != nil {
				t.Fatal(err)
			}
			broken := damage(append([]byte(nil), good...))
			if err := os.WriteFile(s.objectPath(chunk), broken, 0600); err != nil {
				t.Fatal(err)
			}
			for operation, err := range map[string]error{
				"put":            s.Put(ctx, chunk, bytes.NewReader(good)),
				"verify":         s.Verify(ctx, commit),
				"read ref":       func() error { _, err := s.GetRef(ctx, "heads/main"); return err }(),
				"idempotent ref": s.UpdateRef(ctx, "heads/main", nil, commit),
			} {
				if !errors.Is(err, pe.ErrCodeObjectCorrupted) {
					t.Fatalf("%s did not reject corruption: %v", operation, err)
				}
			}
			stored, err := os.ReadFile(s.objectPath(chunk))
			if err != nil || !bytes.Equal(stored, broken) {
				t.Fatal("preexisting corrupt bytes were overwritten:", err)
			}
		})
	}
}

func TestIncompleteGraphsAndCorruptRefsCannotPublish(t *testing.T) {
	byName, _ := corpus(t)
	ctx := context.Background()
	t.Run("missing dependency", func(t *testing.T) {
		s := newStore(t)
		v := byName["commit-old"]
		id := goldenID(t, v)
		if err := s.Put(ctx, id, bytes.NewReader(goldenBytes(t, v))); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateRef(ctx, "heads/main", nil, id); !errors.Is(err, pe.ErrCodeObjectNotFound) {
			t.Fatal("missing graph published:", err)
		}
		assertAbsent(t, s.refPath("heads/main"))
	})
	for _, damage := range []string{"truncated", "wrong-name", "trailing", "bad-kind"} {
		t.Run(damage, func(t *testing.T) {
			s := newStore(t)
			putGoldens(t, s, byName, "commit-new")
			old, next := goldenID(t, byName["commit-old"]), goldenID(t, byName["commit-new"])
			if err := s.UpdateRef(ctx, "heads/main", nil, old); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(s.refPath("heads/main"))
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "truncated":
				data = data[:len(data)-1]
			case "wrong-name":
				data[4] = 'X'
			case "trailing":
				data = append(data, 0)
			case "bad-kind":
				data[len(data)-33] = byte(Chunk)
			}
			if err = os.WriteFile(s.refPath("heads/main"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = s.UpdateRef(ctx, "heads/main", &old, next); !errors.Is(err, pe.ErrCodeObjectCorrupted) {
				t.Fatal("corrupt current ref replaced:", err)
			}
			unchanged, err := os.ReadFile(s.refPath("heads/main"))
			if err != nil || !bytes.Equal(unchanged, data) {
				t.Fatal("corrupt ref overwritten:", err)
			}
		})
	}
}

func TestFormatGuardsNeverUpgradeOrInitializeImplicitly(t *testing.T) {
	byName, _ := corpus(t)
	id := goldenID(t, byName["chunk-a"])
	wire := goldenBytes(t, byName["chunk-a"])
	ctx := context.Background()
	for _, state := range []string{"empty", "v1", "unknown", "partial", "user-files"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			switch state {
			case "v1", "unknown", "partial":
				if err := os.Mkdir(filepath.Join(root, ".pebble"), 0700); err != nil {
					t.Fatal(err)
				}
				if state == "unknown" {
					if err := os.WriteFile(filepath.Join(root, ".pebble", "format"), []byte("pebble-format 3\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if state == "partial" {
					if err := os.WriteFile(filepath.Join(root, ".pebble", "format"), []byte(formatMarker), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "user-files":
				if err := os.WriteFile(filepath.Join(root, "keep"), []byte("user"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Open(root); !errors.Is(err, ErrFormat) {
				t.Fatal("unsupported repository opened:", err)
			}
			if state == "empty" {
				assertAbsent(t, filepath.Join(root, ".pebble"))
				return
			}
			if _, err := Init(ctx, root); !errors.Is(err, pe.ErrCodeAlreadyInitialized) {
				t.Fatal("existing directory initialized:", err)
			}
			if state == "user-files" {
				data, err := os.ReadFile(filepath.Join(root, "keep"))
				if err != nil || string(data) != "user" {
					t.Fatal("user data changed")
				}
			}
		})
	}
	s := newStore(t)
	if err := os.WriteFile(filepath.Join(s.meta, "format"), []byte("pebble-format 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, id, bytes.NewReader(wire)); !errors.Is(err, ErrFormat) {
		t.Fatal("already-open writer ignored changed format:", err)
	}
	assertAbsent(t, s.objectPath(id))
	assertAbsent(t, filepath.Join(s.meta, "write.lock"))
}

func TestStreamingFaultsCancellationAndLimits(t *testing.T) {
	byName, _ := corpus(t)
	v := byName["chunk-binary"]
	id, wire := goldenID(t, v), goldenBytes(t, v)
	fault := errors.New("injected read failure")
	for _, offset := range []int{0, 4, HeaderSize - 1, HeaderSize + 1, len(wire)} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			s := newStore(t)
			if err := s.Put(context.Background(), id, testfixture.FailAfter(wire, offset, fault)); !errors.Is(err, fault) {
				t.Fatal("read fault was lost:", err)
			}
			assertAbsent(t, s.objectPath(id))
			if err := s.Put(context.Background(), id, bytes.NewReader(wire)); err != nil {
				t.Fatal("writer lock/temp not released:", err)
			}
		})
	}
	t.Run("cancel before publication", func(t *testing.T) {
		s := newStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s.fault = func(stage, path string) error {
			if stage == "before-publish" {
				cancel()
			}
			return nil
		}
		if err := s.Put(ctx, id, bytes.NewReader(wire)); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation not honored:", err)
		}
		assertAbsent(t, s.objectPath(id))
		s.fault = nil
		if err := s.Put(context.Background(), id, bytes.NewReader(wire)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("maximum chunk", func(t *testing.T) {
		s := newStore(t)
		payload := testfixture.Noise(MaxChunkBytes)
		id, err := NewID(Chunk, payload)
		if err != nil {
			t.Fatal(err)
		}
		header, err := Header(Chunk, uint64(len(payload)))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Put(context.Background(), id, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(payload))); err != nil {
			t.Fatal(err)
		}
		if err = s.Verify(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPublicationFailuresExposeOnlyCompleteValues(t *testing.T) {
	byName, _ := corpus(t)
	ctx := context.Background()
	fault := errors.New("injected publication failure")
	for _, stage := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			putGoldens(t, s, byName, "commit-new")
			old, next := goldenID(t, byName["commit-old"]), goldenID(t, byName["commit-new"])
			if err := s.UpdateRef(ctx, "heads/main", nil, old); err != nil {
				t.Fatal(err)
			}
			s.fault = func(point, path string) error {
				if point == stage {
					return fault
				}
				return nil
			}
			err := s.UpdateRef(ctx, "heads/main", &old, next)
			if !errors.Is(err, fault) {
				t.Fatal("publication fault acknowledged/lost:", err)
			}
			s.fault = nil
			got, readErr := s.GetRef(ctx, "heads/main")
			if readErr != nil || got != old && got != next {
				t.Fatalf("partial/invalid ref visible: %s %v", got, readErr)
			}
			if stage == "after-publish" && (got != next || !errors.Is(err, ErrPublicationUncertain)) {
				t.Fatal("lost acknowledgement not reported as uncertain")
			}
			if (stage == "file-sync" || stage == "close" || stage == "before-publish") && got != old {
				t.Fatal("ref changed before atomic publication")
			}
			if err = s.UpdateRef(ctx, "heads/main", &old, next); err != nil {
				t.Fatal("safe retry failed:", err)
			}
			assertRef(t, s, "heads/main", next)
		})
		t.Run("object-"+stage, func(t *testing.T) {
			s := newStore(t)
			v := byName["chunk-binary"]
			id, wire := goldenID(t, v), goldenBytes(t, v)
			s.fault = func(point, path string) error {
				if point == stage {
					return fault
				}
				return nil
			}
			err := s.Put(ctx, id, bytes.NewReader(wire))
			if !errors.Is(err, fault) {
				t.Fatal(err)
			}
			if stage == "after-publish" && !errors.Is(err, ErrPublicationUncertain) {
				t.Fatal("object acknowledgement loss not reported as uncertain")
			}
			s.fault = nil
			if _, statErr := os.Lstat(s.objectPath(id)); statErr == nil {
				var restored bytes.Buffer
				if err = s.Copy(ctx, id, &restored); err != nil || !bytes.Equal(restored.Bytes(), wire) {
					t.Fatal("partial object visible:", err)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal(statErr)
			}
			if err = s.Put(ctx, id, bytes.NewReader(wire)); err != nil {
				t.Fatal("object retry:", err)
			}
		})
	}
}

func TestWriterLockAndFilesystemRedirection(t *testing.T) {
	byName, _ := corpus(t)
	ctx := context.Background()
	s := newStore(t)
	id, wire := goldenID(t, byName["chunk-a"]), goldenBytes(t, byName["chunk-a"])
	unlock, err := s.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Put(ctx, id, bytes.NewReader(wire)); !errors.Is(err, ErrLocked) {
		t.Fatal("second store bypassed writer lock:", err)
	}
	if err = unlock(); err != nil {
		t.Fatal(err)
	}
	if err = other.Put(ctx, id, bytes.NewReader(wire)); err != nil {
		t.Fatal(err)
	}
	t.Run("nonregular object", func(t *testing.T) {
		s := newStore(t)
		if err := os.Mkdir(s.objectPath(id), 0700); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, id, bytes.NewReader(wire)); !errors.Is(err, pe.ErrCodeObjectCorrupted) {
			t.Fatal("directory accepted at object address:", err)
		}
	})
	t.Run("object symlink", func(t *testing.T) {
		s := newStore(t)
		target := filepath.Join(s.root, "fixture-target")
		if err := os.WriteFile(target, wire, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, s.objectPath(id)); err != nil {
			t.Skip("symlink privilege unavailable:", err)
		}
		if err := s.Put(ctx, id, bytes.NewReader(wire)); !errors.Is(err, pe.ErrCodeObjectCorrupted) {
			t.Fatal("symlink accepted:", err)
		}
	})
	t.Run("unknown ref catalog file", func(t *testing.T) {
		s := newStore(t)
		putGoldens(t, s, byName, "commit-root")
		if err := os.WriteFile(filepath.Join(s.meta, "refs", "not-a-ref"), []byte("bad"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateRef(ctx, "heads/main", nil, goldenID(t, byName["commit-root"])); !errors.Is(err, pe.ErrCodeObjectCorrupted) {
			t.Fatal("unknown/corrupt ref catalog entry ignored:", err)
		}
		assertAbsent(t, s.refPath("heads/main"))
	})
}

func TestColdRefPublicationReestablishesClosureDurability(t *testing.T) {
	byName, _ := corpus(t)
	ctx := context.Background()
	s := newStore(t)
	putGoldens(t, s, byName, "commit-root")
	root := goldenID(t, byName["commit-root"])
	lost := errors.New("lost object publication acknowledgement")
	s.fault = func(stage, path string) error {
		if stage == "after-publish" && path == s.objectPath(root) {
			return lost
		}
		return nil
	}
	if err := s.Put(ctx, root, bytes.NewReader(goldenBytes(t, byName["commit-root"]))); !errors.Is(err, ErrPublicationUncertain) {
		t.Fatal("object acknowledgement loss not reported:", err)
	}
	s.fault = nil
	cold, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	barrier := errors.New("closure file sync failed")
	cold.fault = func(stage, path string) error {
		if stage == "file-sync" && filepath.Ext(path) == ".obj" {
			return barrier
		}
		return nil
	}
	if err = cold.UpdateRef(ctx, "heads/main", nil, root); !errors.Is(err, barrier) {
		t.Fatal("ref acknowledged without reestablishing cold closure durability:", err)
	}
	assertAbsent(t, cold.refPath("heads/main"))
	cold.fault = nil
	if err = cold.UpdateRef(ctx, "heads/main", nil, root); err != nil {
		t.Fatal(err)
	}
	assertRef(t, cold, "heads/main", root)
}
