// Package testfixture supplies deterministic, format-independent test inputs.
// Callers own the lifetime of disposable directories; this package never removes files.
package testfixture

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const MaxBinaryBytes = 16 << 20

// File is a portable relative path and its complete contents, not an object encoding.
type File struct {
	Path string
	Data []byte
}

// Noise produces reproducible high-entropy bytes using SHA-256 of little-endian
// counters. It does not depend on math/rand implementation or global state.
func Noise(size int) []byte {
	if size < 0 || size > MaxBinaryBytes {
		panic("testfixture: binary size outside 0..16 MiB")
	}
	out := make([]byte, size)
	var counter [8]byte
	for offset := 0; offset < size; offset += sha256.Size {
		binary.LittleEndian.PutUint64(counter[:], uint64(offset/sha256.Size))
		sum := sha256.Sum256(counter[:])
		copy(out[offset:], sum[:])
	}
	return out
}

// Mixed includes empty/tiny/text/Unicode paths, identical copies, zero runs,
// high-entropy binary, and actual encoded assets. binaryBytes is bounded so
// accidental benchmark parameters cannot create unbounded fixtures.
func Mixed(binaryBytes int) ([]File, error) {
	if binaryBytes < 1 || binaryBytes > MaxBinaryBytes {
		return nil, fmt.Errorf("binaryBytes must be in 1..%d", MaxBinaryBytes)
	}
	text := bytes.Repeat([]byte("Pebble fixture: deterministic text, repeated blocks, UTF-8 café.\n"), 1024)
	binaryData := Noise(binaryBytes)
	files := []File{
		{"empty", []byte{}}, {"tiny/one.bin", []byte{0xff}},
		{"text/readme.txt", text}, {"text/copy.txt", bytes.Clone(text)},
		{"nested/space name/café.txt", []byte("line one\r\nline two\n\x00end\n")},
		{"binary/random.bin", binaryData}, {"binary/copy.bin", bytes.Clone(binaryData)},
		{"binary/zeros.bin", make([]byte, binaryBytes)},
	}
	// Structured, noisy RGB image: neither a solid-color compression toy nor
	// copyrighted/user data. Both image formats decode to the same dimensions.
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	pixels := Noise(128 * 128)
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			n := pixels[y*128+x] / 8
			img.SetRGBA(x, y, color.RGBA{uint8(x*2) ^ n, uint8(y*2) ^ n, uint8((x + y)) ^ n, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	files = append(files, File{"assets/scene.png", bytes.Clone(buf.Bytes())})
	buf.Reset()
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	files = append(files, File{"assets/scene.jpg", bytes.Clone(buf.Bytes())})
	buf.Reset()
	gz := gzip.NewWriter(&buf) // zero timestamp, no host filename
	if _, err := gz.Write(text); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	files = append(files, File{"assets/readme.txt.gz", bytes.Clone(buf.Bytes())})
	buf.Reset()
	zw := zip.NewWriter(&buf)
	for _, f := range []File{{"readme.txt", text}, {"payload.bin", binaryData}} {
		w, err := zw.Create(f.Path) // fixed header defaults, no wall-clock time
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return append(files, File{"assets/bundle.zip", bytes.Clone(buf.Bytes())}), nil
}

// Revision changes the same path, preserving history identity independently of
// how an implementation encodes version IDs, chunks, or manifests.
type Revision struct {
	Name string
	File File
}

// Revisions covers identical, in-place edit, insertion, truncation, and empty
// states. Every returned slice is independent; the original is never mutated.
func Revisions(binaryBytes int) []Revision {
	if binaryBytes < 64 || binaryBytes > MaxBinaryBytes {
		panic("testfixture: revision size outside 64..16 MiB")
	}
	base := Noise(binaryBytes)
	edit := bytes.Clone(base)
	for i := binaryBytes / 2; i < binaryBytes/2+32; i++ {
		edit[i] ^= 0xa5
	}
	insert := append(bytes.Clone(base[:binaryBytes/2]), []byte("inserted revision block\n")...)
	insert = append(insert, base[binaryBytes/2:]...)
	return []Revision{
		{"original", File{"binary/repeated.bin", base}},
		{"identical", File{"binary/repeated.bin", bytes.Clone(base)}},
		{"edited", File{"binary/repeated.bin", edit}},
		{"inserted", File{"binary/repeated.bin", insert}},
		{"truncated", File{"binary/repeated.bin", bytes.Clone(base[:binaryBytes/3])}},
		{"empty", File{"binary/repeated.bin", []byte{}}},
	}
}

// Materialize writes only the supplied files under a caller-owned disposable
// root, creating directories as needed. It is not a synchronization/deletion API.
func Materialize(root string, files []File) error {
	for _, f := range files {
		if !fs.ValidPath(f.Path) || strings.ContainsAny(f.Path, "\\:") {
			return fmt.Errorf("unsafe fixture path %q", f.Path)
		}
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, f.Data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func TotalBytes(files []File) int64 {
	var total int64
	for _, f := range files {
		total += int64(len(f.Data))
	}
	return total
}
