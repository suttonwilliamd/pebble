// Package processlock serializes cooperating processes without stale ownership
// after termination. Guard files are persistent: never unlink a live guard.
package processlock

import (
	"errors"
	"fmt"
	"os"
)

func Acquire(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	opened, statErr := f.Stat()
	if err != nil || statErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.Join(err, statErr, f.Close(), fmt.Errorf("invalid process guard: %s", path))
	}
	if err = lock(f); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, current) {
		return nil, errors.Join(err, f.Close(), fmt.Errorf("process guard replaced: %s", path))
	}
	return func() error { return errors.Join(unlock(f), f.Close()) }, nil
}
