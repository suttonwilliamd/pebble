package storev2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
)

const (
	manifestPageEntries = 1024
	maxManifestPages    = 16384
	manifestLinkBytes   = 41
	manifestPrefixBytes = 44
)

// ChunkRef identifies one nonempty, already-stored chunk and its exact raw size.
// Repetitions are meaningful: each occurrence contributes bytes in that order.
type ChunkRef struct {
	ID     ID
	Length uint64
}

// CreateManifest consumes ordered chunk references until next returns io.EOF.
// Any other error aborts, including an error returned alongside a reference.
// It verifies actual stored bytes and derives the raw whole-file checksum; the
// caller chooses chunk boundaries and stores chunks with Put, not this method.
//
// Construction retains only a bounded root, one page and a reusable I/O buffer.
// It publishes canonical immutable pages/root, never a pathname binding or ref.
// On failure, unreferenced pages (or a root after an uncertain publication) may
// remain. Discard the returned ID on error; retry with the same chunk sequence.
func (s *Store) CreateManifest(ctx context.Context, next func() (ChunkRef, error)) (ID, error) {
	if err := s.guard(); err != nil {
		return ID{}, err
	}
	if next == nil {
		return ID{}, invalid("chunk-source")
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	root := make([]byte, manifestPrefixBytes, manifestPrefixBytes+manifestLinkBytes)
	page := make([]byte, 4, 4+manifestPageEntries*manifestLinkBytes)
	h := sha256.New()
	var total, pageLength uint64
	var pageCount, chunkCount uint32
	flush := func() error {
		binary.BigEndian.PutUint32(page[:4], chunkCount)
		id, err := s.putPayload(ctx, ManifestPage, page)
		if err != nil {
			return err
		}
		root = appendManifestLink(root, id, pageLength)
		pageCount++
		page, chunkCount, pageLength = page[:4], 0, 0
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return ID{}, err
		}
		ref, err := next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ID{}, storageError("construct manifest", err)
		}
		if pageCount == maxManifestPages {
			return ID{}, invalid("limit")
		}
		if ref.ID.Kind != Chunk {
			return ID{}, corrupt(ref.ID, invalid("id-type"))
		}
		if ref.Length == 0 || ref.Length > MaxChunkBytes || ref.Length > maxLength-total {
			return ID{}, corrupt(ref.ID, invalid("length"))
		}
		n, err := s.load(ctx, ref.ID, nil, h, buffer[:], nil)
		if err != nil {
			return ID{}, err
		}
		if n.size != ref.Length {
			return ID{}, corrupt(ref.ID, invalid("length"))
		}
		total += ref.Length
		pageLength += ref.Length
		page = appendManifestLink(page, ref.ID, ref.Length)
		chunkCount++
		if chunkCount == manifestPageEntries {
			if err := flush(); err != nil {
				return ID{}, err
			}
		}
	}
	if chunkCount != 0 {
		if err := flush(); err != nil {
			return ID{}, err
		}
	}
	binary.BigEndian.PutUint64(root[:8], total)
	h.Sum(root[8:8])
	binary.BigEndian.PutUint32(root[40:44], pageCount)
	return s.putPayload(ctx, Manifest, root)
}

// appendManifestLink appends the frozen wire link and checked length. Callers
// enforce page/root counts before growing their bounded metadata payloads.
func appendManifestLink(payload []byte, id ID, length uint64) []byte {
	payload = append(payload, byte(id.Kind))
	payload = append(payload, id.Digest[:]...)
	return binary.BigEndian.AppendUint64(payload, length)
}

func (s *Store) putPayload(ctx context.Context, kind Kind, payload []byte) (ID, error) {
	id, err := NewID(kind, payload)
	if err != nil {
		return ID{}, err
	}
	header, err := Header(kind, uint64(len(payload)))
	if err != nil {
		return ID{}, err
	}
	if err := s.Put(ctx, id, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(payload))); err != nil {
		return ID{}, err
	}
	return id, nil
}

// CopyFile streams the raw bytes of one immutable manifest revision. It verifies
// every typed address, canonical page partition, exact length and whole-file
// checksum. Repeated pages/chunks are read in order, not deduplicated as bytes.
// Output is provisional until nil error: discard it on any failure. This is not
// a crash-atomic destination writer and does not create or update pathname refs.
func (s *Store) CopyFile(ctx context.Context, id ID, dst io.Writer) error {
	if id.Kind != Manifest {
		return corrupt(id, invalid("id-type"))
	}
	if err := s.guard(); err != nil {
		return err
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	n, err := s.load(ctx, id, nil, nil, buffer[:], nil)
	if err != nil {
		return err
	}
	return s.copyManifest(ctx, id, n, dst, buffer[:], nil, nil)
}

// Graph verification can reuse its already-validated page metadata. Standalone
// file reads load only one page at a time; chunk bytes are always revalidated as
// delivered to the checksum/destination, never served from a validation cache.
func (s *Store) copyManifest(ctx context.Context, id ID, n *node, dst io.Writer, buffer []byte, budget *uint64, pages map[ID]*node) error {
	h := sha256.New()
	var out io.Writer = h
	if dst != nil {
		out = io.MultiWriter(h, dst)
	}
	for i, e := range n.edges {
		page := pages[e.id]
		if page == nil {
			var err error
			page, err = s.load(ctx, e.id, nil, nil, buffer, budget)
			if err != nil {
				return err
			}
		}
		if page.total != e.length {
			return corrupt(id, invalid("length"))
		}
		if i != len(n.edges)-1 && len(page.edges) != manifestPageEntries {
			return corrupt(id, invalid("partition"))
		}
		for _, chunk := range page.edges {
			actual, err := s.load(ctx, chunk.id, nil, out, buffer, budget)
			if err != nil {
				return err
			}
			if actual.size != chunk.length {
				return corrupt(e.id, invalid("length"))
			}
		}
	}
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	if sum != n.checksum {
		return corrupt(id, invalid("checksum"))
	}
	return ctx.Err()
}
