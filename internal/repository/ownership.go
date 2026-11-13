package repository

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sync"
)

var worktreeBuffers = sync.Pool{New: func() any { return new([maxLinkBytes + 1]byte) }}

// Hide optional WriterTo so io.CopyBuffer actually reuses the supplied buffer
// on both supported Go toolchains rather than allocating a file fallback buffer.
type workingReader struct{ io.Reader }

func hashWorkingReader(source io.Reader) (n int64, sum [32]byte, err error) {
	buffer := worktreeBuffers.Get().(*[maxLinkBytes + 1]byte)
	defer worktreeBuffers.Put(buffer)
	h := sha256.New()
	n, err = io.CopyBuffer(h, workingReader{source}, buffer[:])
	if err == nil {
		h.Sum(sum[:0])
	}
	return
}
func sameWorkingInfo(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime() == b.ModTime()
}
func verifyLinkImage(link linkContent, image *fileImage) error {
	if uint64(len(link.target)) != image.Size || sha256.Sum256([]byte(link.target)) != image.Sum || measuresDirectoryLink && link.directory != image.DirectoryLink {
		return fmt.Errorf("owned symbolic-link content/type changed")
	}
	return nil
}
func verifyOpenedImage(f *os.File, image *fileImage) error {
	before, err := f.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || uint64(before.Size()) != image.Size || before.ModTime().UnixNano() != image.Modified || (before.Mode()&0111 != 0) != image.Executable {
		return fmt.Errorf("owned file metadata changed: %s", f.Name())
	}
	_, sum, err := hashWorkingReader(f)
	if err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if sum != image.Sum || !sameWorkingInfo(before, after) {
		return fmt.Errorf("owned file content changed: %s", f.Name())
	}
	return nil
}
