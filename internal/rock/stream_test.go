package rock

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"reflect"
	"testing"
	"testing/iotest"
)

func TestStreamMatchesBatch(t *testing.T) {
	for _, size := range []int{0, 1, 47, 48, 49, 511, 512, 8191, 8192, 8193, 65536, 131099} {
		data := make([]byte, size)
		rand.New(rand.NewSource(int64(size))).Read(data)
		for _, zero := range []bool{false, true} {
			if zero {
				clear(data)
			}
			for _, short := range []bool{false, true} {
				f := DefaultFastCDC()
				want, err := f.Chunk(data)
				if err != nil {
					t.Fatal(err)
				}
				var reader io.Reader = bytes.NewReader(data)
				if short {
					reader = iotest.OneByteReader(reader)
				}
				var got []Chunk
				err = f.ChunkStream(reader, func(c Chunk) error { got = append(got, c); return nil })
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(ReconstructData(got), data) {
					t.Fatalf("stream lost data: size=%d zero=%v short=%v", size, zero, short)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("batch/stream boundaries differ: size=%d zero=%v short=%v", size, zero, short)
				}
			}
		}
	}
}
func TestStreamErrors(t *testing.T) {
	sentinel := errors.New("stop")
	if err := DefaultFastCDC().ChunkStream(bytes.NewReader([]byte("small")), func(Chunk) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error: %v", err)
	}
	if err := DefaultFastCDC().ChunkStream(iotest.ErrReader(sentinel), func(Chunk) error { return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("read error: %v", err)
	}
	for _, f := range []*FastCDC{NewFastCDC(0, 8192, 48), NewFastCDC(512, 0, 48), NewFastCDC(512, 8192, 0), NewFastCDC(512, 8192, 513)} {
		if _, err := f.Chunk([]byte("x")); err == nil {
			t.Fatal("invalid batch configuration accepted")
		}
		if err := f.ChunkStream(bytes.NewReader([]byte("x")), func(Chunk) error { return nil }); err == nil {
			t.Fatal("invalid stream configuration accepted")
		}
	}
}

func FuzzStreamRoundTrip(f *testing.F) {
	f.Add([]byte("small input"))
	f.Add(make([]byte, 9000))
	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewFastCDC(64, 1024, 16)
		want, err := c.Chunk(data)
		if err != nil {
			t.Fatal(err)
		}
		var got []Chunk
		if err = c.ChunkStream(bytes.NewReader(data), func(chunk Chunk) error { got = append(got, chunk); return nil }); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ReconstructData(got), data) || !reflect.DeepEqual(got, want) {
			t.Fatal("stream/batch roundtrip mismatch")
		}
	})
}
