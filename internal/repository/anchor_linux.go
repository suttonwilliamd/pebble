package repository

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameAnchoredExclusive(from int, source string, to int, target string) error {
	return unix.Renameat2(from, source, to, target, unix.RENAME_NOREPLACE)
}
func (a *pathAnchor) openLink() (*os.File, error) {
	fd, err := unix.Openat(a.parent, a.leaf, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), a.leaf), nil
}
