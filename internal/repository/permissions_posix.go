//go:build !windows

package repository

import (
	"errors"
	"golang.org/x/sys/unix"
)

func preflightDirectory(path string, writable bool) (err error) {
	a, err := anchorPath(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	f, err := a.openDirectory()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if writable {
		if err = unix.Faccessat(int(f.Fd()), ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS); err != nil {
			return err
		}
	}
	return f.Sync()
}
