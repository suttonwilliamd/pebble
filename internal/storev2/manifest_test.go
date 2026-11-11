package storev2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	pe "github.com/suttonwilliamd/pebble/internal/errors"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func putManifestObject(t *testing.T, s *Store, kind Kind, payload []byte) ID {
	t.Helper()
	id, err := NewID(kind, payload)
	if err != nil {
		t.Fatal(err)
	}
	header, err := Header(kind, uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), id, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(payload))); err != nil {
		t.Fatal(err)
	}
	return id
}

func manifestFromRefs(t *testing.T, s *Store, refs []ChunkRef) ID {
	t.Helper()
	i := 0
	id, err := s.CreateManifest(context.Background(), func() (ChunkRef, error) {
		if i == len(refs) {
			return ChunkRef{}, io.EOF
		}
		ref := refs[i]
		i++
		return ref, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// This test input policy is deliberately not a production chunker. PB-05 chooses
// its own boundaries; the manifest API accepts arbitrary ordered chunk sizes.
func manifestFromBytes(t *testing.T, s *Store, data []byte, width int) ID {
	t.Helper()
	offset := 0
	id, err := s.CreateManifest(context.Background(), func() (ChunkRef, error) {
		if offset == len(data) {
			return ChunkRef{}, io.EOF
		}
		end := offset + width
		if end > len(data) {
			end = len(data)
		}
		part := data[offset:end]
		offset = end
		return ChunkRef{putManifestObject(t, s, Chunk, part), uint64(len(part))}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func manifestWire(t *testing.T, s *Store, id ID) []byte {
	t.Helper()
	var wire bytes.Buffer
	if err := s.Copy(context.Background(), id, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

// Test-side decoding uses the frozen byte positions, not the production parser.
func manifestTestLink(b []byte) ID {
	id := ID{Kind: Kind(b[0])}
	copy(id.Digest[:], b[1:33])
	return id
}

func assertManifestBytes(t *testing.T, s *Store, id ID, want []byte) {
	t.Helper()
	var out bytes.Buffer
	if err := s.CopyFile(context.Background(), id, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatal("historical raw bytes differ")
	}
	payload := manifestWire(t, s, id)[HeaderSize:]
	sum := sha256.Sum256(want)
	if binary.BigEndian.Uint64(payload[:8]) != uint64(len(want)) || !bytes.Equal(payload[8:40], sum[:]) {
		t.Fatal("manifest length/raw whole-file checksum differ from independent input")
	}
}

func TestManifestConstructionMatchesFrozenGoldens(t *testing.T) {
	byName, vectors := corpus(t)
	byID := make(map[ID]golden)
	for _, v := range vectors {
		if v.Error == "" && v.ID != "" {
			byID[goldenID(t, v)] = v
		}
	}
	for _, v := range vectors {
		if v.Error != "" || v.ID == "" || goldenID(t, v).Kind != Manifest {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			s := newStore(t)
			root := goldenBytes(t, v)[HeaderSize:]
			var refs []ChunkRef
			var want []byte
			var pages []golden
			stored := make(map[ID]bool)
			for offset := 44; offset < len(root); offset += 41 {
				pageGolden := byID[manifestTestLink(root[offset:])]
				pages = append(pages, pageGolden)
				page := goldenBytes(t, pageGolden)[HeaderSize:]
				for pos := 4; pos < len(page); pos += 41 {
					chunkID := manifestTestLink(page[pos:])
					chunk := byID[chunkID]
					if !stored[chunkID] {
						putGoldens(t, s, byName, chunk.Name)
						stored[chunkID] = true
					}
					refs = append(refs, ChunkRef{chunkID, binary.BigEndian.Uint64(page[pos+33 : pos+41])})
					want = append(want, goldenBytes(t, chunk)[HeaderSize:]...)
				}
			}
			got := manifestFromRefs(t, s, refs)
			if got != goldenID(t, v) || !bytes.Equal(manifestWire(t, s, got), goldenBytes(t, v)) {
				t.Fatal("constructed root differs from frozen bytes/address")
			}
			for _, page := range pages {
				if !bytes.Equal(manifestWire(t, s, goldenID(t, page)), goldenBytes(t, page)) {
					t.Fatal("constructed page differs from frozen bytes")
				}
			}
			if err := s.Verify(context.Background(), got); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			assertManifestBytes(t, reopened, got, want)
		})
	}
}

func TestManifestMixedFilesAndPathHistoryAfterReopen(t *testing.T) {
	s := newStore(t)
	files, err := testfixture.Mixed(128 << 10)
	if err != nil {
		t.Fatal(err)
	}
	type revision struct {
		file testfixture.File
		root ID
		tree ID
	}
	var history []revision
	for _, r := range testfixture.Revisions(96 << 10) {
		files = append(files, r.File)
	}
	for _, file := range files {
		root := manifestFromBytes(t, s, file.Data, 16381)
		// Bind the same repeated pathname to separate immutable tree objects.
		payload := make([]byte, 8+len(file.Path)+1+33)
		binary.BigEndian.PutUint32(payload[:4], 1)
		binary.BigEndian.PutUint32(payload[4:8], uint32(len(file.Path)))
		copy(payload[8:], file.Path)
		payload[9+len(file.Path)] = byte(Manifest)
		copy(payload[10+len(file.Path):], root.Digest[:])
		tree := putManifestObject(t, s, Tree, payload)
		if err := s.Verify(context.Background(), tree); err != nil {
			t.Fatal(err)
		}
		history = append(history, revision{file, root, tree})
	}
	// Identical revisions preserve identity; edits/insertion/truncation/empty
	// revisions cannot replace the earlier tree's root or append to its chunks.
	base := len(history) - 6
	if history[base].root != history[base+1].root || history[base].tree != history[base+1].tree {
		t.Fatal("identical revisions lost content identity")
	}
	for i := base + 2; i < len(history); i++ {
		if history[i].root == history[base].root || history[i].tree == history[base].tree {
			t.Fatal("changed revision reused old identity")
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
			pathLength := int(binary.BigEndian.Uint32(tree[4:8]))
			root := manifestTestLink(tree[9+pathLength:])
			if string(tree[8:8+pathLength]) != r.file.Path || root != r.root {
				t.Fatal("immutable pathname binding changed")
			}
			assertManifestBytes(t, reopened, root, r.file.Data)
		})
	}
}

func TestManifestRevisionsShareOnlyUnchangedPages(t *testing.T) {
	byName, _ := corpus(t)
	s := newStore(t)
	putGoldens(t, s, byName, "chunk-a", "chunk-binary")
	a, b := goldenID(t, byName["chunk-a"]), goldenID(t, byName["chunk-binary"])
	refs := make([]ChunkRef, 1025)
	for i := range refs {
		refs[i] = ChunkRef{a, 1}
	}
	old := manifestFromRefs(t, s, refs)
	refs[1024] = ChunkRef{b, 5}
	next := manifestFromRefs(t, s, refs)
	oldWire, nextWire := manifestWire(t, s, old)[HeaderSize:], manifestWire(t, s, next)[HeaderSize:]
	if old == next || manifestTestLink(oldWire[44:]) != manifestTestLink(nextWire[44:]) || manifestTestLink(oldWire[85:]) == manifestTestLink(nextWire[85:]) {
		t.Fatal("revision failed to share unchanged full page and replace changed final page")
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	assertManifestBytes(t, reopened, next, append(bytes.Repeat([]byte("A"), 1024), 0, 255, 10, 13, 128))
	assertManifestBytes(t, reopened, old, bytes.Repeat([]byte("A"), 1025))
}

func TestManifestRejectsInvalidChunkSources(t *testing.T) {
	byName, _ := corpus(t)
	a := goldenID(t, byName["chunk-a"])
	missing := a
	missing.Digest[0] ^= 0xff
	for _, tc := range []struct {
		name string
		ref  ChunkRef
		want error
	}{
		{"missing", ChunkRef{missing, 1}, pe.ErrCodeObjectNotFound},
		{"wrong-kind", ChunkRef{goldenID(t, byName["tree-empty"]), 4}, ErrInvalidObject},
		{"empty", ChunkRef{goldenID(t, byName["chunk-empty"]), 0}, ErrInvalidObject},
		{"oversized", ChunkRef{a, MaxChunkBytes + 1}, ErrInvalidObject},
		{"wrong-length", ChunkRef{a, 2}, ErrInvalidObject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			putGoldens(t, s, byName, "chunk-a", "chunk-empty", "tree-empty")
			id, err := s.CreateManifest(context.Background(), func() (ChunkRef, error) { return tc.ref, nil })
			if id != (ID{}) || !errors.Is(err, tc.want) {
				t.Fatalf("invalid chunk accepted: %s %v", id, err)
			}
		})
	}
	fault := errors.New("chunk source failed")
	for _, count := range []int{0, 1, 1024} {
		t.Run(fmt.Sprintf("source-error-after-%d", count), func(t *testing.T) {
			s := newStore(t)
			putGoldens(t, s, byName, "chunk-a")
			i := 0
			id, err := s.CreateManifest(context.Background(), func() (ChunkRef, error) {
				if i == count {
					return ChunkRef{a, 1}, fault
				}
				i++
				return ChunkRef{a, 1}, nil
			})
			if id != (ID{}) || !errors.Is(err, fault) {
				t.Fatal("source error acknowledged/lost:", id, err)
			}
			name := map[int]string{0: "manifest-empty", 1: "manifest-old", 1024: "manifest-1024"}[count]
			if err := s.Copy(context.Background(), goldenID(t, byName[name]), io.Discard); !errors.Is(err, pe.ErrCodeObjectNotFound) {
				t.Fatal("failed construction published a root:", err)
			}
		})
	}
}

func TestCopyFileRejectsMissingOrDamagedObjects(t *testing.T) {
	byName, _ := corpus(t)
	ctx := context.Background()
	for _, missing := range []string{"manifest-new", "page-binary", "chunk-binary"} {
		t.Run("missing/"+missing, func(t *testing.T) {
			s := newStore(t)
			for _, name := range []string{"manifest-new", "page-binary", "chunk-binary"} {
				if name != missing {
					v := byName[name]
					if err := s.Put(ctx, goldenID(t, v), bytes.NewReader(goldenBytes(t, v))); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.CopyFile(ctx, goldenID(t, byName["manifest-new"]), io.Discard); !errors.Is(err, pe.ErrCodeObjectNotFound) {
				t.Fatal("incomplete file accepted:", err)
			}
		})
	}
	for _, target := range []string{"manifest-new", "page-binary", "chunk-binary"} {
		for _, damage := range []string{"short", "extra", "wrong-kind", "wrong-address"} {
			t.Run(target+"/"+damage, func(t *testing.T) {
				s := newStore(t)
				putGoldens(t, s, byName, "manifest-new")
				v := byName[target]
				wire := goldenBytes(t, v)
				switch damage {
				case "short":
					wire = wire[:len(wire)-1]
				case "extra":
					wire = append(wire, 0)
				case "wrong-kind":
					wire[4] = byte(Tree)
				case "wrong-address":
					wire[len(wire)-1] ^= 1
				}
				if err := os.WriteFile(s.objectPath(goldenID(t, v)), wire, 0600); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				if err := reopened.CopyFile(ctx, goldenID(t, byName["manifest-new"]), io.Discard); !errors.Is(err, pe.ErrCodeObjectCorrupted) {
					t.Fatal("damaged file accepted:", err)
				}
				emitted := false
				id, err := reopened.CreateManifest(ctx, func() (ChunkRef, error) {
					if emitted {
						return ChunkRef{}, io.EOF
					}
					emitted = true
					return ChunkRef{goldenID(t, byName["chunk-binary"]), 5}, nil
				})
				if id != (ID{}) || !errors.Is(err, pe.ErrCodeObjectCorrupted) {
					t.Fatal("constructor accepted damaged immutable object:", id, err)
				}
				unchanged, err := os.ReadFile(s.objectPath(goldenID(t, v)))
				if err != nil || !bytes.Equal(unchanged, wire) {
					t.Fatal("constructor replaced existing corrupt bytes:", err)
				}
			})
		}
	}
}

func TestCopyFileRejectsSemanticManifestFailures(t *testing.T) {
	byName, _ := corpus(t)
	t.Run("chunk-length", func(t *testing.T) {
		s := newStore(t)
		v := byName["page-chunk-size-mismatch"]
		putGoldens(t, s, byName, v.Dependencies...)
		if err := s.Put(context.Background(), goldenID(t, v), bytes.NewReader(goldenBytes(t, v))); err != nil {
			t.Fatal(err)
		}
		root := bytes.Clone(goldenBytes(t, byName["manifest-old"])[HeaderSize:])
		binary.BigEndian.PutUint64(root[:8], 2)
		pageID := goldenID(t, v)
		copy(root[45:77], pageID.Digest[:])
		binary.BigEndian.PutUint64(root[77:85], 2)
		id := putManifestObject(t, s, Manifest, root)
		if err := s.CopyFile(context.Background(), id, io.Discard); !errors.Is(err, ErrInvalidObject) {
			t.Fatal("wrong actual chunk length accepted:", err)
		}
	})
	t.Run("out-of-order-chunks", func(t *testing.T) {
		s := newStore(t)
		a := putManifestObject(t, s, Chunk, []byte("A"))
		b := putManifestObject(t, s, Chunk, []byte("B"))
		good := manifestFromRefs(t, s, []ChunkRef{{a, 1}, {b, 1}})
		root := bytes.Clone(manifestWire(t, s, good)[HeaderSize:])
		pageID := manifestTestLink(root[44:])
		page := bytes.Clone(manifestWire(t, s, pageID)[HeaderSize:])
		left := bytes.Clone(page[4:45])
		copy(page[4:45], page[45:86])
		copy(page[45:86], left)
		wrongPage := putManifestObject(t, s, ManifestPage, page)
		copy(root[45:77], wrongPage.Digest[:])
		wrong := putManifestObject(t, s, Manifest, root)
		if err := s.CopyFile(context.Background(), wrong, io.Discard); !errors.Is(err, ErrInvalidObject) {
			t.Fatal("out-of-order file accepted:", err)
		}
		assertManifestBytes(t, s, good, []byte("AB"))
	})
}

// Checks actual streamed bytes without accumulating the reconstructed file.
type manifestCheckingWriter struct {
	chunk  []byte
	offset int
}

func (w *manifestCheckingWriter) Write(p []byte) (int, error) {
	if len(p) > 32<<10 || w.offset+len(p) > 2*len(w.chunk) {
		return 0, fmt.Errorf("unbounded or extra output at %d", w.offset)
	}
	for i, b := range p {
		if b != w.chunk[(w.offset+i)%len(w.chunk)] {
			return 0, fmt.Errorf("wrong byte at %d", w.offset+i)
		}
	}
	w.offset += len(p)
	return len(p), nil
}

func TestManifestStreamsRepeatedMaximumChunks(t *testing.T) {
	s := newStore(t)
	chunk := testfixture.Noise(MaxChunkBytes)
	id := putManifestObject(t, s, Chunk, chunk)
	root := manifestFromRefs(t, s, []ChunkRef{{id, MaxChunkBytes}, {id, MaxChunkBytes}})
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	out := &manifestCheckingWriter{chunk: chunk}
	if err := reopened.CopyFile(context.Background(), root, out); err != nil {
		t.Fatal(err)
	}
	if out.offset != 2*len(chunk) {
		t.Fatal("short streamed file")
	}
	h := sha256.New()
	h.Write(chunk)
	h.Write(chunk)
	payload := manifestWire(t, reopened, root)[HeaderSize:]
	if binary.BigEndian.Uint64(payload[:8]) != uint64(2*len(chunk)) || !bytes.Equal(payload[8:40], h.Sum(nil)) {
		t.Fatal("incorrect streamed file metadata")
	}
}

type manifestFailWriter struct {
	remaining int
	fault     error
}

func (w *manifestFailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.remaining {
		n = w.remaining
	}
	w.remaining -= n
	if w.remaining == 0 {
		return n, w.fault
	}
	return n, nil
}

func TestManifestWriterErrorsAndCancellation(t *testing.T) {
	byName, _ := corpus(t)
	s := newStore(t)
	putGoldens(t, s, byName, "manifest-new")
	root := goldenID(t, byName["manifest-new"])
	fault := errors.New("destination failed")
	for _, offset := range []int{0, 1, 5} {
		if err := s.CopyFile(context.Background(), root, &manifestFailWriter{offset, fault}); !errors.Is(err, fault) {
			t.Fatal("destination error acknowledged/lost:", err)
		}
	}
	if err := s.CopyFile(context.Background(), root, &manifestFailWriter{1, nil}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short write accepted:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.CopyFile(ctx, root, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read accepted:", err)
	}
	if id, err := s.CreateManifest(ctx, func() (ChunkRef, error) { return ChunkRef{}, io.EOF }); id != (ID{}) || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled construction accepted:", id, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if id, err := s.CreateManifest(ctx, func() (ChunkRef, error) {
		cancel()
		return ChunkRef{}, io.EOF
	}); id != (ID{}) || !errors.Is(err, context.Canceled) {
		t.Fatal("end-of-source cancellation published a root:", id, err)
	}
	if err := s.CopyFile(context.Background(), goldenID(t, byName["chunk-binary"]), io.Discard); !errors.Is(err, ErrInvalidObject) {
		t.Fatal("raw chunk treated as a file revision:", err)
	}
	assertManifestBytes(t, s, root, []byte{0, 255, 10, 13, 128})
}
