package repository

import (
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func renameAnchoredExclusive(from int, source string, to int, target string) error {
	a, err := unix.BytePtrFromString(source)
	if err != nil {
		return err
	}
	b, err := unix.BytePtrFromString(target)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(unix.SYS_RENAMEATX_NP, uintptr(from), uintptr(unsafe.Pointer(a)), uintptr(to), uintptr(unsafe.Pointer(b)), 4, 0)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	if errno != 0 {
		return errno
	}
	return nil
}
func (a *pathAnchor) openLink() (*os.File, error) {
	fd, err := unix.Openat(a.parent, a.leaf, unix.O_RDONLY|unix.O_SYMLINK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), a.leaf), nil
}
