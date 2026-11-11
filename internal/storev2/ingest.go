package storev2

import (
	"context"
	"io"

	"github.com/suttonwilliamd/pebble/internal/rock"
)

// IngestFile streams arbitrary raw bytes into typed chunks and an immutable
// paged manifest. A nil policy selects ROCK's default FastCDC policy. Chunk
// sizes must obey the canonical 8 MiB bound; the policy is snapshotted before
// reading. No filename, text conversion, compression or pathname index is used.
//
// Only one borrowed chunk buffer, one manifest page and the bounded root are
// retained, in addition to the store's bounded publication/validation state.
// Chunks are published synchronously before their ChunkRef is consumed by
// CreateManifest; neither method holds an outer writer lock.
//
// No tree/ref is changed. On any failure the ID is zero; completed unreferenced
// objects may remain, including a root after an uncertain publication. Retry the
// same input/policy. Cancellation cannot interrupt an arbitrary blocking reader.
func (s *Store) IngestFile(ctx context.Context, source io.Reader, policy *rock.FastCDC) (ID, error) {
	if err := ctx.Err(); err != nil {
		return ID{}, err
	}
	if err := s.guard(); err != nil {
		return ID{}, err
	}
	if policy == nil {
		policy = rock.DefaultFastCDC()
	}
	if policy.MaxChunkSize > MaxChunkBytes {
		return ID{}, invalid("chunk-size-limit")
	}
	if source == nil {
		return ID{}, invalid("chunk-source")
	}
	stream, err := policy.NewChunkReader(&contextReader{ctx: ctx, reader: source})
	if err != nil {
		return ID{}, err
	}
	return s.CreateManifest(ctx, func() (ChunkRef, error) {
		data, err := stream.Next()
		if err != nil {
			return ChunkRef{}, err
		}
		id, err := s.putPayload(ctx, Chunk, data)
		if err != nil {
			return ChunkRef{}, err
		}
		return ChunkRef{ID: id, Length: uint64(len(data))}, nil
	})
}
