package testfixture

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMixedCorpus(t *testing.T) {
	files, err := Mixed(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Mixed(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, again) {
		t.Fatal("nondeterministic corpus")
	}
	root := t.TempDir()
	if err := Materialize(root, files); err != nil {
		t.Fatal(err)
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil || !bytes.Equal(got, f.Data) {
			t.Fatalf("materialize %s: %v", f.Path, err)
		}
		byPath[f.Path] = f.Data
		t.Logf("%s: %d bytes sha256=%x", f.Path, len(f.Data), sha256.Sum256(f.Data))
	}
	t.Logf("mixed corpus: %d files, %d bytes", len(files), TotalBytes(files))
	for _, name := range []string{"assets/scene.png", "assets/scene.jpg"} {
		var width, height int
		if name == "assets/scene.png" {
			img, err := png.Decode(bytes.NewReader(byPath[name]))
			if err != nil {
				t.Fatal(err)
			}
			width, height = img.Bounds().Dx(), img.Bounds().Dy()
		} else {
			img, err := jpeg.Decode(bytes.NewReader(byPath[name]))
			if err != nil {
				t.Fatal(err)
			}
			width, height = img.Bounds().Dx(), img.Bounds().Dy()
		}
		if width != 128 || height != 128 {
			t.Fatalf("invalid image dimensions %s: %dx%d", name, width, height)
		}
	}
	gz, err := gzip.NewReader(bytes.NewReader(byPath["assets/readme.txt.gz"]))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, byPath["text/readme.txt"]) {
		t.Fatal("gzip content mismatch")
	}
	z, err := zip.NewReader(bytes.NewReader(byPath["assets/bundle.zip"]), int64(len(byPath["assets/bundle.zip"])))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{"readme.txt": byPath["text/readme.txt"], "payload.bin": byPath["binary/random.bin"]}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		expected, ok := want[f.Name]
		if !ok || !bytes.Equal(got, expected) {
			t.Fatalf("zip entry %q content mismatch", f.Name)
		}
		delete(want, f.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing zip entries: %v", want)
	}
}

func TestRevisionsPreserveIndependentHistory(t *testing.T) {
	revs := Revisions(96)
	base := bytes.Clone(revs[0].File.Data)
	if !bytes.Equal(revs[1].File.Data, base) {
		t.Fatal("identical revision differs")
	}
	if bytes.Equal(revs[2].File.Data, base) {
		t.Fatal("edit did not alter content")
	}
	if !bytes.Equal(revs[3].File.Data[:48], base[:48]) || !bytes.Equal(revs[3].File.Data[len(revs[3].File.Data)-48:], base[48:]) {
		t.Fatal("insertion lost surrounding bytes")
	}
	if !bytes.Equal(revs[4].File.Data, base[:32]) || len(revs[5].File.Data) != 0 {
		t.Fatal("truncate/empty revision incorrect")
	}
	for _, rev := range revs {
		if rev.File.Path != revs[0].File.Path {
			t.Fatal("revision changed path")
		}
	}
	revs[1].File.Data[0] ^= 1
	revs[2].File.Data[0] ^= 1
	revs[3].File.Data[0] ^= 1
	revs[4].File.Data[0] ^= 1
	if !bytes.Equal(revs[0].File.Data, base) {
		t.Fatal("later revision mutated original")
	}
}

func TestCorpusBoundsAndContainment(t *testing.T) {
	for _, size := range []int{-1, 0, MaxBinaryBytes + 1} {
		if _, err := Mixed(size); err == nil {
			t.Fatalf("accepted size %d", size)
		}
	}
	for _, path := range []string{"../escape", "/absolute", "C:/escape", "a\\b"} {
		if err := Materialize(t.TempDir(), []File{{path, []byte("x")}}); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
}

func TestFailAfterPreservesPartialReadError(t *testing.T) {
	fault := errors.New("injected I/O failure")
	for _, offset := range []int{0, 1, 7, 8, 9} {
		data := []byte("12345678")
		got, err := io.ReadAll(FailAfter(data, offset, fault))
		if offset <= len(data) {
			if !errors.Is(err, fault) || !bytes.Equal(got, data[:offset]) {
				t.Fatalf("offset %d: %q %v", offset, got, err)
			}
		} else if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("ordinary EOF: %q %v", got, err)
		}
	}
}
