package storev2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	pe "github.com/suttonwilliamd/pebble/internal/errors"
	"github.com/suttonwilliamd/pebble/internal/rock"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func ingestBytes(t *testing.T, s *Store, data []byte) ID {
	t.Helper()
	id, err := s.IngestFile(context.Background(), bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ingestionChunks(t *testing.T, s *Store, root ID) []ID {
	t.Helper()
	payload := manifestWire(t, s, root)[HeaderSize:]
	var ids []ID
	for pos := 44; pos < len(payload); pos += 41 {
		page := manifestWire(t, s, manifestTestLink(payload[pos:]))[HeaderSize:]
		for i := 4; i < len(page); i += 41 {
			ids = append(ids, manifestTestLink(page[i:]))
		}
	}
	return ids
}

func TestIngestMixedAndHistoricalRevisions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	files, err := testfixture.Mixed(96 << 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range testfixture.Revisions(128 << 10) {
		files = append(files, revision.File)
	}
	type saved struct {
		file       testfixture.File
		root, tree ID
	}
	var history []saved
	for _, file := range files {
		root := ingestBytes(t, s, file.Data)
		payload := make([]byte, 8+len(file.Path)+1+33)
		binary.BigEndian.PutUint32(payload[:4], 1)
		binary.BigEndian.PutUint32(payload[4:8], uint32(len(file.Path)))
		copy(payload[8:], file.Path)
		payload[9+len(file.Path)] = byte(Manifest)
		copy(payload[10+len(file.Path):], root.Digest[:])
		tree := putManifestObject(t, s, Tree, payload)
		if err := s.Verify(ctx, tree); err != nil {
			t.Fatal(err)
		}
		history = append(history, saved{file, root, tree})
	}
	// Different filenames, identical bytes: same root and physical chunk IDs.
	if history[3].root != history[2].root || history[5].root != history[6].root {
		t.Fatal("cross-file deduplication failed")
	}
	base := len(history) - 6
	if history[base].root != history[base+1].root || history[base].tree != history[base+1].tree {
		t.Fatal("identical revision lost identity")
	}
	original := ingestionChunks(t, s, history[base].root)
	seen := make(map[ID]bool)
	for _, id := range original {
		seen[id] = true
	}
	for _, index := range []int{base + 2, base + 3} {
		shared, changed := false, false
		for _, id := range ingestionChunks(t, s, history[index].root) {
			if seen[id] {
				shared = true
			} else {
				changed = true
			}
		}
		if !shared || !changed {
			t.Fatal("edit/insertion did not reuse unchanged chunks and retain changed bytes")
		}
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(history) - 1; i >= 0; i-- {
		r := history[i]
		t.Run(fmt.Sprintf("%d/%s", i, r.file.Path), func(t *testing.T) {
			tree := manifestWire(t, reopened, r.tree)[HeaderSize:]
			n := int(binary.BigEndian.Uint32(tree[4:8]))
			root := manifestTestLink(tree[9+n:])
			if string(tree[8:8+n]) != r.file.Path || root != r.root {
				t.Fatal("historical pathname binding changed")
			}
			assertManifestBytes(t, reopened, root, r.file.Data)
		})
	}
}

func TestIngestFragmentationAndCanonicalBounds(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	data := testfixture.Noise(65539)
	want := ingestBytes(t, s, data)
	for _, r := range []io.Reader{iotest.OneByteReader(bytes.NewReader(data)), iotest.HalfReader(bytes.NewReader(data)), &terminalReader{data, io.EOF}} {
		got, err := s.IngestFile(ctx, r, nil)
		if err != nil || got != want {
			t.Fatal("reader fragmentation changed revision:", got, err)
		}
	}
	for _, policy := range []*rock.FastCDC{rock.NewFastCDC(0, 8192, 48), rock.NewFastCDC(512, MaxChunkBytes+1, 48), rock.NewFastCDC(512, 8192, 513)} {
		if id, err := s.IngestFile(ctx, bytes.NewReader(data), policy); id != (ID{}) || err == nil {
			t.Fatal("invalid policy accepted:", id, err)
		}
	}
	if id, err := s.IngestFile(ctx, nil, nil); id != (ID{}) || err == nil {
		t.Fatal("nil source accepted:", id, err)
	}
	// Exact canonical maximum must be accepted, not split or copied whole-file.
	data = testfixture.Noise(MaxChunkBytes)
	policy := rock.NewFastCDC(MaxChunkBytes, MaxChunkBytes, 48)
	id, err := s.IngestFile(ctx, bytes.NewReader(data), policy)
	if err != nil {
		t.Fatal(err)
	}
	chunks := ingestionChunks(t, s, id)
	chunk, err := NewID(Chunk, data)
	if err != nil || len(chunks) != 1 || chunks[0] != chunk {
		t.Fatal("canonical maximum chunk changed:", err)
	}
	assertManifestBytes(t, s, id, data)
}

func assertNoIngestRoot(t *testing.T, s *Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.meta, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "03-") {
			t.Fatal("failed input published a manifest root")
		}
	}
}

func TestIngestReaderFailuresNeverAcknowledge(t *testing.T) {
	fault := errors.New("input failed")
	for _, size := range []int{0, 1, 8192, 8193, 16386} {
		data := testfixture.Noise(size)
		for _, failure := range []error{fault, io.ErrUnexpectedEOF} {
			for _, sticky := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%v/sticky=%v", size, failure, sticky), func(t *testing.T) {
					s := newStore(t)
					var r io.Reader = &terminalReader{data, failure}
					if sticky || size == 0 {
						r = testfixture.FailAfter(data, size, failure)
					}
					id, err := s.IngestFile(context.Background(), r, nil)
					if id != (ID{}) || !errors.Is(err, failure) {
						t.Fatal("reader failure acknowledged/lost:", id, err)
					}
					assertNoIngestRoot(t, s)
					assertManifestBytes(t, s, ingestBytes(t, s, data), data)
				})
			}
		}
	}
}

func TestIngestPublicationFailuresAndCancellation(t *testing.T) {
	fault := errors.New("publication failed")
	data := testfixture.Noise(32769)
	for _, kind := range []Kind{Chunk, ManifestPage, Manifest} {
		for _, stage := range []string{"file-sync", "close", "before-publish", "namespace-sync", "after-publish"} {
			t.Run(kind.String()+"/"+stage, func(t *testing.T) {
				s := newStore(t)
				s.fault = func(at, path string) error {
					if at == stage && strings.HasPrefix(filepath.Base(path), fmt.Sprintf("%02x-", byte(kind))) {
						return fault
					}
					return nil
				}
				id, err := s.IngestFile(context.Background(), bytes.NewReader(data), nil)
				if id != (ID{}) || !errors.Is(err, fault) {
					t.Fatal("publication error acknowledged/lost:", id, err)
				}
				if stage == "after-publish" && !errors.Is(err, ErrPublicationUncertain) {
					t.Fatal("lost acknowledgement not uncertain:", err)
				}
				s.fault = nil
				id = ingestBytes(t, s, data)
				reopened, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				assertManifestBytes(t, reopened, id, data)
			})
		}
	}
	for _, at := range []string{"before-read", "during-read", "root-publication"} {
		t.Run(at, func(t *testing.T) {
			s := newStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var source io.Reader = bytes.NewReader(data)
			if at == "before-read" {
				cancel()
			}
			if at == "during-read" {
				source = &ingestCancelReader{bytes.NewReader(data), cancel}
			}
			if at == "root-publication" {
				s.fault = func(stage, path string) error {
					if stage == "before-publish" && strings.HasPrefix(filepath.Base(path), "03-") {
						cancel()
					}
					return nil
				}
			}
			id, err := s.IngestFile(ctx, source, nil)
			if id != (ID{}) || !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation acknowledged/lost:", id, err)
			}
			assertNoIngestRoot(t, s)
		})
	}
}

type ingestCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *ingestCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestIngestNeverRepairsCorruptSharedChunk(t *testing.T) {
	s := newStore(t)
	data := testfixture.Noise(32769)
	old := ingestBytes(t, s, data)
	chunk := ingestionChunks(t, s, old)[0]
	wire := manifestWire(t, s, chunk)
	wire[len(wire)-1] ^= 1
	if err := os.WriteFile(s.objectPath(chunk), wire, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := reopened.IngestFile(context.Background(), bytes.NewReader(data), nil)
	if id != (ID{}) || !errors.Is(err, pe.ErrCodeObjectCorrupted) {
		t.Fatal("corrupt dedup candidate accepted:", id, err)
	}
	if err := reopened.CopyFile(context.Background(), old, io.Discard); !errors.Is(err, pe.ErrCodeObjectCorrupted) {
		t.Fatal("corrupt history restored:", err)
	}
	unchanged, err := os.ReadFile(s.objectPath(chunk))
	if err != nil || !bytes.Equal(unchanged, wire) {
		t.Fatal("ingestion silently repaired corruption:", err)
	}
}
