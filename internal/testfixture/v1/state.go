package v1fixture

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type FileState struct {
	Mode     os.FileMode
	Size     int64
	Modified time.Time
	Checksum [32]byte
	Link     string
}

// Fingerprint reads a caller-owned disposable fixture without following links.
// Access times are deliberately excluded because reading may update them.
func Fingerprint(root string) (map[string]FileState, error) {
	state := map[string]FileState{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			// NTFS enumeration can return cached directory write times from
			// before fixture construction completed. Query an opened handle,
			// not the parent enumeration's cached Win32FindData snapshot.
			directory, err := os.Open(path)
			if err != nil {
				return err
			}
			info, err = directory.Stat()
			err = errors.Join(err, directory.Close())
			if err != nil {
				return err
			}
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file := FileState{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			file.Link, err = os.Readlink(path)
		case info.Mode().IsRegular():
			var f *os.File
			f, err = os.Open(path)
			if err != nil {
				return err
			}
			digest := sha256.New()
			_, err = io.Copy(digest, f)
			err = errors.Join(err, f.Close())
			copy(file.Checksum[:], digest.Sum(nil))
		}
		if err != nil {
			return err
		}
		state[filepath.ToSlash(relative)] = file
		return nil
	})
	return state, err
}
