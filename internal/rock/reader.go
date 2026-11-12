package rock

import (
	"fmt"
	"io"
)

// ChunkReader selects the same boundaries as FastCDC.Chunk using one reusable
// MaxChunkSize+1 byte buffer. It is sequential, not safe for concurrent use.
type ChunkReader struct {
	policy  FastCDC
	reader  io.Reader
	buffer  []byte
	power   uint32
	total   int
	used    int
	eof     bool
	failure error
}

// NewChunkReader validates and snapshots the policy. Later changes to f cannot
// affect this stream. No input is read until Next.
func (f *FastCDC) NewChunkReader(reader io.Reader) (*ChunkReader, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("nil chunk reader")
	}
	return &ChunkReader{
		policy: *f, reader: reader, buffer: make([]byte, f.MaxChunkSize+1),
		power: f.primePower(),
	}, nil
}

// Next returns a nonempty borrowed slice, valid only until the next Next call.
// Consume it synchronously or copy it. EOF is returned only after all valid
// final bytes have been delivered. Any non-EOF read error is sticky and fatal,
// even alongside a full read; no chunk from that failing read is acknowledged.
func (r *ChunkReader) Next() ([]byte, error) {
	if r.failure != nil {
		return nil, r.failure
	}
	r.total = copy(r.buffer, r.buffer[r.used:r.total])
	r.used = 0
	emptyReads := 0
	for !r.eof && r.total < len(r.buffer) {
		n, err := r.reader.Read(r.buffer[r.total:])
		if n < 0 || n > len(r.buffer)-r.total {
			r.failure = fmt.Errorf("invalid reader byte count: %d", n)
			return nil, r.failure
		}
		r.total += n
		if err != nil && err != io.EOF {
			r.failure = err
			return nil, err
		}
		if err == io.EOF {
			r.eof = true
		}
		if n == 0 && err == nil {
			emptyReads++
			if emptyReads == 100 {
				r.failure = io.ErrNoProgress
				return nil, r.failure
			}
		} else {
			emptyReads = 0
		}
	}
	if r.total == 0 {
		return nil, io.EOF
	}
	r.used = r.policy.boundary(r.buffer[:r.total], r.power)
	return r.buffer[:r.used:r.used], nil
}
