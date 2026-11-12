package storev2

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pe "github.com/suttonwilliamd/pebble/internal/errors"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func packAll(t *testing.T, s *Store) (string, []ID) {
	t.Helper()
	ctx := context.Background()
	ids, err := s.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, err := s.Pack(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	return name, ids
}
func removeFixtureLoose(t *testing.T, s *Store, ids []ID) {
	t.Helper()
	// Only this test's TempDir fixture is stripped; production never reclaims.
	for _, id := range ids {
		if err := os.Remove(s.objectPath(id)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestPackOnlyReopenGraphsAndRebuild(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	byName, _ := corpus(t)
	putGoldens(t, s, byName, "commit-merge", "tag-annotated", "manifest-new")
	tip := goldenID(t, byName["commit-merge"])
	if err := s.UpdateRef(ctx, "heads/main", nil, tip); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef(ctx, "tags/v2.0", nil, goldenID(t, byName["tag-annotated"])); err != nil {
		t.Fatal(err)
	}
	name, ids := packAll(t, s)
	want := make(map[ID][]byte, len(ids))
	for _, id := range ids {
		var b bytes.Buffer
		if err := s.Copy(ctx, id, &b); err != nil {
			t.Fatal(err)
		}
		want[id] = append([]byte(nil), b.Bytes()...)
	}
	removeFixtureLoose(t, s, ids)
	for _, damage := range []string{"intact", "absent", "checksum", "offset"} {
		t.Run(damage, func(t *testing.T) {
			index := s.packPath(name, ".idx")
			if damage == "absent" {
				if err := os.Remove(index); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "checksum" || damage == "offset" {
				b, err := os.ReadFile(index)
				if err != nil {
					t.Fatal(err)
				}
				if damage == "checksum" {
					b[len(b)-1] ^= 1
				} else {
					binary.BigEndian.PutUint64(b[94:102], 1)
					b = checkedBytes(b[:len(b)-32])
				}
				if err = os.WriteFile(index, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, readErr := os.ReadFile(index)
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			for id, wire := range want {
				var b bytes.Buffer
				if err = reopened.Copy(ctx, id, &b); err != nil || !bytes.Equal(b.Bytes(), wire) {
					t.Fatalf("envelope %s: %v", id, err)
				}
			}
			if err = reopened.Verify(ctx, tip); err != nil {
				t.Fatal(err)
			}
			assertRef(t, reopened, "heads/main", tip)
			after, afterErr := os.ReadFile(index)
			if !bytes.Equal(before, after) || errors.Is(readErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
				t.Fatal("read repaired metadata on disk")
			}
			// Cold ref publication must establish pack/index/catalog barriers, not
			// silently regenerate loose objects to avoid those barriers.
			seen := map[string]bool{}
			reopened.fault = func(stage, path string) error {
				if stage == "after-publish" {
					seen[filepath.Ext(path)] = true
					if filepath.Base(path) == "pack-catalog" {
						seen["catalog"] = true
					}
				}
				return nil
			}
			if err = reopened.UpdateRef(ctx, "heads/main", nil, tip); err != nil {
				t.Fatal(err)
			}
			if !seen[".pack"] || !seen[".idx"] || !seen["catalog"] || !seen[".ref"] {
				t.Fatal("missing cold physical barrier", seen)
			}
			for _, id := range ids {
				assertAbsent(t, s.objectPath(id))
			}
			if err = reopened.RebuildPackIndexes(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := reopened.Inventory(ctx)
			if err != nil || len(got) != len(ids) {
				t.Fatal(got, err)
			}
			for i := range ids {
				if ids[i] != got[i] {
					t.Fatal("inventory changed IDs")
				}
			}
		})
	}
}

func TestPackPB02MixedRawBytesAndCompression(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	files, err := testfixture.Mixed(512 << 10)
	if err != nil {
		t.Fatal(err)
	}
	roots := make([]ID, len(files))
	for i, f := range files {
		roots[i], err = s.IngestFile(ctx, bytes.NewReader(f.Data), nil)
		if err != nil {
			t.Fatal(f.Path, err)
		}
	}
	name, ids := packAll(t, s)
	p, err := s.scanPack(ctx, name, true, make([]byte, 32<<10))
	if err != nil {
		t.Fatal(err)
	}
	codecs := map[byte]int{}
	for _, e := range p.entries {
		codecs[e.codec]++
	}
	if codecs[0] == 0 || codecs[1] == 0 {
		t.Fatal("corpus did not exercise raw and compressed records", codecs)
	}
	removeFixtureLoose(t, s, ids)
	if err = os.Remove(s.packPath(name, ".idx")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range files {
		var b bytes.Buffer
		if err = reopened.CopyFile(ctx, roots[i], &b); err != nil || !bytes.Equal(b.Bytes(), f.Data) {
			t.Fatalf("%s raw mismatch: %v", f.Path, err)
		}
	}
	for _, id := range ids {
		var wire bytes.Buffer
		if err = reopened.Copy(ctx, id, &wire); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(wire.Bytes())
		if sum != id.Digest {
			t.Fatal("compression changed logical address")
		}
	}
	if err = reopened.RebuildPackIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("PB02 files=%d objects=%d raw-records=%d zlib-records=%d", len(files), len(ids), codecs[0], codecs[1])
}

// publishMalformedPack gives adversarial framing a correct physical checksum
// and filename, so logical validation, not merely checksum failure, must reject it.
func publishMalformedPack(t *testing.T, s *Store, b []byte) string {
	t.Helper()
	sum := sha256.Sum256(b)
	name := hex.EncodeToString(sum[:])
	b = append(b, sum[:]...)
	if err := os.WriteFile(s.packPath(name, ".pack"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}
func zipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zlib.NewWriter(&out)
	if err := writeAll(z, b); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func recordBytes(id ID, codec byte, raw uint64, data []byte) []byte {
	b := make([]byte, recordHeaderSize+len(data))
	b[0] = byte(id.Kind)
	copy(b[1:33], id.Digest[:])
	b[33] = codec
	binary.BigEndian.PutUint64(b[34:42], raw)
	binary.BigEndian.PutUint64(b[42:50], uint64(len(data)))
	copy(b[50:], data)
	return b
}
func TestPackMalformedNeverPublishesDerivedMetadata(t *testing.T) {
	byName, _ := corpus(t)
	id := goldenID(t, byName["chunk-a"])
	wire := goldenBytes(t, byName["chunk-a"])
	head := make([]byte, 12)
	copy(head, packMagic)
	binary.BigEndian.PutUint32(head[8:], 1)
	record := recordBytes(id, 0, uint64(len(wire)), wire)
	cases := map[string][]byte{}
	cases["truncated"] = append(append([]byte(nil), head...), record[:len(record)-1]...)
	cases["trailing"] = append(append(append([]byte(nil), head...), record...), 0)
	wrong := id
	wrong.Digest[0] ^= 1
	cases["misaddressed"] = append(append([]byte(nil), head...), recordBytes(wrong, 0, uint64(len(wire)), wire)...)
	bad := append([]byte(nil), wire...)
	bad[4] = byte(Tree)
	cases["typed-kind"] = append(append([]byte(nil), head...), recordBytes(id, 0, uint64(len(bad)), bad)...)
	cases["oversize"] = append(append([]byte(nil), head...), recordBytes(id, 0, MaxChunkBytes+HeaderSize+1, wire)...)
	cases["codec"] = append(append([]byte(nil), head...), recordBytes(id, 2, uint64(len(wire)), wire)...)
	cases["compressed-bomb"] = append(append([]byte(nil), head...), recordBytes(id, 1, uint64(len(wire)), zipped(t, bytes.Repeat([]byte("a"), 1<<20)))...)
	cases["compressed-trailing"] = append(append([]byte(nil), head...), recordBytes(id, 1, uint64(len(wire)), append(zipped(t, wire), 0))...)
	duplicate := append([]byte(nil), head...)
	binary.BigEndian.PutUint32(duplicate[8:], 2)
	duplicate = append(duplicate, record...)
	duplicate = append(duplicate, record...)
	cases["duplicate"] = duplicate
	for label, b := range cases {
		t.Run(label, func(t *testing.T) {
			s := newStore(t)
			name := publishMalformedPack(t, s, b)
			if err := s.RebuildPackIndexes(context.Background()); err == nil {
				t.Fatal("malformed pack accepted")
			}
			assertAbsent(t, s.packPath(name, ".idx"))
			assertAbsent(t, s.packPath("catalog", ""))
			var out bytes.Buffer
			if err := s.Copy(context.Background(), id, &out); err == nil {
				t.Fatal("malformed packed read accepted")
			}
		})
	}
}

func TestCorruptPackCannotBeMaskedOrReindexed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	byName, _ := corpus(t)
	putGoldens(t, s, byName, "commit-old")
	name, ids := packAll(t, s)
	index, err := os.ReadFile(s.packPath(name, ".idx"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := os.ReadFile(s.packPath("catalog", ""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.packPath(name, ".pack"))
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-33] ^= 1
	if err = os.WriteFile(s.packPath(name, ".pack"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Inventory(ctx); err == nil {
		t.Fatal("loose copies masked corrupt pack")
	}
	if err = s.Verify(ctx, goldenID(t, byName["commit-old"])); err == nil {
		t.Fatal("loose graph masked corrupt pack")
	}
	if err = s.RebuildPackIndexes(ctx); err == nil {
		t.Fatal("reindexed corrupt pack")
	}
	for path, want := range map[string][]byte{s.packPath(name, ".idx"): index, s.packPath("catalog", ""): catalog} {
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("published false metadata", path, e)
		}
	}
	removeFixtureLoose(t, s, ids)
	tip := goldenID(t, byName["commit-old"])
	if err = s.UpdateRef(ctx, "heads/bad", nil, tip); err == nil {
		t.Fatal("corrupt packed graph published")
	}
	assertAbsent(t, s.refPath("heads/bad"))
}

func TestPackPublicationFaultRetry(t *testing.T) {
	for _, ext := range []string{".pack", ".idx", "pack-catalog"} {
		for _, stage := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
			t.Run(ext+"/"+stage, func(t *testing.T) {
				s := newStore(t)
				byName, _ := corpus(t)
				putGoldens(t, s, byName, "commit-old")
				ctx := context.Background()
				ids, err := s.Inventory(ctx)
				if err != nil {
					t.Fatal(err)
				}
				injected := errors.New("boundary failure")
				hit := false
				s.fault = func(point, path string) error {
					if point == stage && (filepath.Ext(path) == ext || filepath.Base(path) == ext) {
						hit = true
						return injected
					}
					return nil
				}
				if _, err = s.Pack(ctx, ids); !errors.Is(err, injected) || !hit {
					t.Fatal("fault not propagated", err)
				}
				s.fault = nil
				reopened, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = reopened.Pack(ctx, ids); err != nil {
					t.Fatal("identical retry", err)
				}
				if err = reopened.Verify(ctx, goldenID(t, byName["commit-old"])); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestPackProcessHelper(t *testing.T) {
	if os.Getenv("PEBBLE_PACK_HELPER") == "" {
		return
	}
	s, err := Open(os.Getenv("PEBBLE_PACK_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	boundary := os.Getenv("PEBBLE_PACK_BOUNDARY")
	s.fault = func(stage, path string) error {
		if stage == "after-publish" && (filepath.Ext(path) == boundary || filepath.Base(path) == boundary) {
			os.Exit(74)
		}
		return nil
	}
	ids, err := s.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pack(context.Background(), ids)
	t.Fatal("did not interrupt", err)
}
func TestPackProcessInterruptionRecovery(t *testing.T) {
	for _, boundary := range []string{".pack", ".idx", "pack-catalog"} {
		t.Run(boundary, func(t *testing.T) {
			s := newStore(t)
			byName, _ := corpus(t)
			putGoldens(t, s, byName, "commit-old")
			ctx := context.Background()
			ids, err := s.Inventory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			childCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(childCtx, executable, "-test.run=^TestPackProcessHelper$")
			cmd.Env = append(os.Environ(), "PEBBLE_PACK_HELPER=1", "PEBBLE_PACK_ROOT="+s.root, "PEBBLE_PACK_BOUNDARY="+boundary)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 74 {
				t.Fatalf("%v: %s", err, out)
			}
			removeFixtureLoose(t, s, ids)
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			if err = reopened.Verify(ctx, goldenID(t, byName["commit-old"])); err != nil {
				t.Fatal("pack lost at interruption", err)
			}
			if err = reopened.RebuildPackIndexes(ctx); !errors.Is(err, ErrLocked) {
				t.Fatal("stale writer lock removed", err)
			}
			// The child has exited. Explicit fixture-only recovery, not automatic lock stealing.
			if err = os.Remove(filepath.Join(s.meta, "write.lock")); err != nil {
				t.Fatal(err)
			}
			if err = reopened.RebuildPackIndexes(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.Pack(ctx, ids); err != nil {
				t.Fatal(err)
			}
			if err = reopened.UpdateRef(ctx, "heads/recovered", nil, goldenID(t, byName["commit-old"])); err != nil {
				t.Fatal(err)
			}
			assertRef(t, reopened, "heads/recovered", goldenID(t, byName["commit-old"]))
		})
	}
}

func TestLooseOnlyOpenDoesNotCreatePackMetadata(t *testing.T) {
	s := newStore(t)
	byName, _ := corpus(t)
	putGoldens(t, s, byName, "commit-old")
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Verify(context.Background(), goldenID(t, byName["commit-old"])); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, s.packPath("catalog", ""))
	if _, err = reopened.Inventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, s.packPath("catalog", ""))
	id := goldenID(t, byName["chunk-a"])
	if _, err = reopened.Pack(context.Background(), []ID{id, id}); !errors.Is(err, ErrInvalidObject) {
		t.Fatal(err)
	}
	if err = reopened.Copy(context.Background(), ID{Kind: Chunk}, &bytes.Buffer{}); !errors.Is(err, pe.ErrCodeObjectNotFound) {
		t.Fatal(err)
	}
}
