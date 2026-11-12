package repository

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func renameMigrationExclusive(source, destination string) error {
	from, err := unix.BytePtrFromString(source)
	if err != nil {
		return err
	}
	to, err := unix.BytePtrFromString(destination)
	if err != nil {
		return err
	}
	// x/sys v0.20 exposes SYS_RENAMEATX_NP but not its wrapper. Darwin's
	// AT_FDCWD is -2 and RENAME_EXCL is 0x4; never fall back to replacing rename.
	_, _, errno := syscall.Syscall6(unix.SYS_RENAMEATX_NP, ^uintptr(1), uintptr(unsafe.Pointer(from)), ^uintptr(1), uintptr(unsafe.Pointer(to)), 0x4, 0)
	runtime.KeepAlive(from)
	runtime.KeepAlive(to)
	if errno != 0 {
		return errno
	}
	return nil
}
