package rock

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"testing/iotest"

	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func TestChunkReaderBoundariesAndBorrowing(t *testing.T) {
	data := testfixture.Noise(131099)
	for _, size := range []int{0, 1, 47, 48, 49, 511, 512, 8191, 8192, 8193, len(data)} {
		for _, wrap := range []func(io.Reader) io.Reader{
			func(r io.Reader) io.Reader { return r }, iotest.OneByteReader, iotest.HalfReader, iotest.DataErrReader,
		} {
			policy := DefaultFastCDC()
			want, err := policy.Chunk(data[:size])
			if err != nil {
				t.Fatal(err)
			}
			r, err := policy.NewChunkReader(wrap(bytes.NewReader(data[:size])))
			if err != nil {
				t.Fatal(err)
			}
			// The active stream must not observe later policy changes.
			policy.MinChunkSize = 0
			var lengths []int
			var first *byte
			offset := 0
			for {
				chunk, err := r.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(chunk) == 0 || len(chunk) > 8192 || !bytes.Equal(chunk, data[offset:offset+len(chunk)]) {
					t.Fatalf("wrong chunk at %d for input %d", offset, size)
				}
				if first == nil {
					first = &chunk[0]
				} else if first != &chunk[0] {
					t.Fatal("chunk buffer was not reused")
				}
				lengths = append(lengths, len(chunk))
				offset += len(chunk)
			}
			var expected []int
			for _, c := range want {
				expected = append(expected, len(c.Content))
			}
			if offset != size || !reflect.DeepEqual(lengths, expected) {
				t.Fatalf("fragmentation changed boundaries: %d", size)
			}
		}
	}
	// Independently specified zero/nonzero lookahead boundary: average=8,
	// first zero at 9, next byte nonzero, so cut after byte 9 (length 10).
	policy := NewFastCDC(4, 12, 2)
	policy.Mask, policy.Target = 0, 1 // disable primary hash matches
	input := bytes.Repeat([]byte{1}, 25)
	input[9] = 0
	r, err := policy.NewChunkReader(iotest.OneByteReader(bytes.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	var lengths []int
	for {
		b, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		lengths = append(lengths, len(b))
	}
	if !reflect.DeepEqual(lengths, []int{10, 12, 3}) {
		t.Fatal("lookahead policy changed:", lengths)
	}
	policy.Target = 0 // mask=0 now matches at every eligible position
	r, err = policy.NewChunkReader(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	lengths = nil
	for {
		b, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		lengths = append(lengths, len(b))
	}
	if !reflect.DeepEqual(lengths, []int{4, 4, 4, 4, 4, 4, 1}) {
		t.Fatal("primary minimum boundary changed:", lengths)
	}
}

type chunkTerminalReader struct {
	data     []byte
	terminal error
}

func (r *chunkTerminalReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.terminal
	}
	return n, nil
}

func TestChunkReaderTerminalFailures(t *testing.T) {
	fault := errors.New("final bytes failed")
	for _, size := range []int{1, 8192, 8193, 16386} {
		for _, failure := range []error{fault, io.ErrUnexpectedEOF} {
			for _, callback := range []bool{false, true} {
				source := &chunkTerminalReader{testfixture.Noise(size), failure}
				var err error
				if callback {
					err = DefaultFastCDC().ChunkStream(source, func(Chunk) error { return nil })
				} else {
					r, e := DefaultFastCDC().NewChunkReader(source)
					if e != nil {
						t.Fatal(e)
					}
					for {
						_, err = r.Next()
						if err != nil {
							break
						}
					}
					if _, again := r.Next(); !errors.Is(again, failure) {
						t.Fatal("terminal failure was not sticky:", again)
					}
				}
				if !errors.Is(err, failure) {
					t.Fatalf("lost final-bytes error size=%d: %v", size, err)
				}
			}
		}
	}
	if _, err := DefaultFastCDC().NewChunkReader(nil); err == nil {
		t.Fatal("nil input accepted")
	}
	r, err := DefaultFastCDC().NewChunkReader(chunkEmptyReader{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); !errors.Is(err, io.ErrNoProgress) {
		t.Fatal("non-progressing reader accepted:", err)
	}
}

type chunkEmptyReader struct{}

func (chunkEmptyReader) Read([]byte) (int, error) { return 0, nil }
