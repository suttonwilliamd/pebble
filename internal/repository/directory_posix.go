//go:build !windows

package repository

import (
	"golang.org/x/sys/unix"
	"os"
)

func (a *pathAnchor) openDirectory() (*os.File, error) {
	fd, err := unix.Openat(a.parent, a.leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), a.leaf), nil
}
