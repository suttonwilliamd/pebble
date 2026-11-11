package storev2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/rock"
)

// Counter-hash bytes match testfixture.Noise, but are generated a block at a
// time. Neither the reader nor the checking writer retains the complete input.
type ingestGeneratedReader struct {
	remaining uint64
	offset    uint64
	maxRead   int
	reads     int
	peakHeap  uint64
	sample    bool
}

func (r *ingestGeneratedReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	if r.sample && r.reads%32 == 0 {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		if stats.HeapAlloc > r.peakHeap {
			r.peakHeap = stats.HeapAlloc
		}
	}
	r.reads++
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if uint64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	written := 0
	for written < len(p) {
		var counter [8]byte
		binary.LittleEndian.PutUint64(counter[:], r.offset/sha256.Size)
		block := sha256.Sum256(counter[:])
		n := copy(p[written:], block[r.offset%sha256.Size:])
		written += n
		r.offset += uint64(n)
		r.remaining -= uint64(n)
	}
	return written, nil
}

type ingestGeneratedWriter struct {
	expected ingestGeneratedReader
	buffer   [32 << 10]byte
	written  uint64
	maxWrite int
}

func (w *ingestGeneratedWriter) Write(p []byte) (int, error) {
	if len(p) > len(w.buffer) {
		return 0, fmt.Errorf("restore exceeded 32 KiB I/O bound: %d", len(p))
	}
	if len(p) > w.maxWrite {
		w.maxWrite = len(p)
	}
	n, err := io.ReadFull(&w.expected, w.buffer[:len(p)])
	if err != nil || !bytes.Equal(p, w.buffer[:n]) {
		return 0, fmt.Errorf("generated restore mismatch at %d: %v", w.written, err)
	}
	w.written += uint64(n)
	return n, nil
}

func TestIngestGeneratedStream(t *testing.T) {
	for _, size := range []uint64{1 << 20, 32 << 20} {
		t.Run(fmt.Sprintf("%dMiB", size>>20), func(t *testing.T) {
			s := newStore(t)
			policy := rock.NewFastCDC(8<<10, 32<<10, 48)
			source := &ingestGeneratedReader{remaining: size, sample: true}
			h := sha256.New()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			id, err := s.IngestFile(context.Background(), io.TeeReader(source, h), policy)
			if err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			if after.HeapAlloc > source.peakHeap {
				source.peakHeap = after.HeapAlloc
			}
			if source.offset != size || source.remaining != 0 || source.maxRead > policy.MaxChunkSize+1 {
				t.Fatal("unbounded or short generated input")
			}
			payload := manifestWire(t, s, id)[HeaderSize:]
			if binary.BigEndian.Uint64(payload[:8]) != size || !bytes.Equal(payload[8:40], h.Sum(nil)) {
				t.Fatal("generated manifest checksum/length mismatch")
			}
			pages := binary.BigEndian.Uint32(payload[40:44])
			if size == 32<<20 && pages < 2 {
				t.Fatal("generated scenario failed to cross manifest page boundary")
			}
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			out := &ingestGeneratedWriter{expected: ingestGeneratedReader{remaining: size}}
			if err := reopened.CopyFile(context.Background(), id, out); err != nil {
				t.Fatal(err)
			}
			if out.written != size || out.expected.remaining != 0 {
				t.Fatal("short generated restore")
			}
			t.Logf("input=%d pages=%d max-read-request=%d max-restore-write=%d chunk-buffer=%d sampled-Go-heap-baseline=%d sampled-Go-heap-peak=%d post-ingest-Go-heap=%d total-allocated-delta=%d; sampled heap is not RSS or an exact peak", size, pages, source.maxRead, out.maxWrite, policy.MaxChunkSize+1, before.HeapAlloc, source.peakHeap, after.HeapAlloc, after.TotalAlloc-before.TotalAlloc)
		})
	}
}
