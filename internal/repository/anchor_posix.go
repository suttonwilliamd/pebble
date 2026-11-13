//go:build !windows

package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type pathAnchor struct {
	handles []int
	parent  int
	leaf    string
}

func anchorPath(path string, _ bool) (_ *pathAnchor, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	a := &pathAnchor{}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.close())
		}
	}()
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	a.handles = append(a.handles, fd)
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		fd, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		a.handles = append(a.handles, fd)
	}
	a.parent, a.leaf = fd, parts[len(parts)-1]
	return a, nil
}
func (a *pathAnchor) close() (err error) {
	for i := len(a.handles) - 1; i >= 0; i-- {
		err = errors.Join(err, unix.Close(a.handles[i]))
	}
	return err
}
func (a *pathAnchor) openRead() (*os.File, error) {
	fd, err := unix.Openat(a.parent, a.leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), a.leaf), nil
}
func statIdentity(st *unix.Stat_t) string { return fmt.Sprintf("%x:%x", st.Dev, st.Ino) }

// POSIX rename cannot predicate the source inode. If the unavoidable final
// syscall window exposes substitution, reverse without replacing ANY public
// entry. A blocked reversal remains pending for explicit automatic reconciliation;
// private retention alone is not called canonical public restoration.
func restoreUnexpectedMove(from, to *pathAnchor, source, target, moved string, reason error) error {
	var st unix.Stat_t
	if err := unix.Fstatat(to.parent, to.leaf, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil || statIdentity(&st) != moved {
		return errors.Join(fmt.Errorf("rename recovery ownership changed; public %s and retained %s require identity-checked pebble recover", source, target), reason, err)
	}
	if err := renameAnchoredExclusive(to.parent, to.leaf, from.parent, from.leaf); err != nil {
		return errors.Join(fmt.Errorf("cannot immediately restore source directory entry %s without changing intervening work; unexpected identity %s retained at %s; pebble recover will reconcile without replacement or retain a receipted alternate", source, moved, target), reason, err)
	}
	syncErr := errors.Join(unix.Fsync(from.parent), unix.Fsync(to.parent))
	return errors.Join(fmt.Errorf("source entry returned through its pinned original parent after a rename race; pebble recover will reconcile any rebound public parent of %s: %w", source, reason), syncErr)
}
func anchoredMove(source, target, identity string, image *fileImage, publicSource bool, check, before, after, parents, barrier func() error) (err error) {
	from, err := anchorPath(source, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, from.close()) }()
	to, err := anchorPath(target, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, to.close()) }()
	if err = check(); err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstatat(from.parent, from.leaf, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if statIdentity(&st) != identity {
		return fmt.Errorf("source identity changed: %s", source)
	}
	var opened *os.File
	if image == nil {
		opened, err = from.openDirectory()
	} else if image.Symlink {
		opened, err = from.openLink()
	} else {
		opened, err = from.openRead()
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, opened.Close()) }()
	if err = unix.Fstat(int(opened.Fd()), &st); err != nil {
		return err
	}
	if statIdentity(&st) != identity {
		return fmt.Errorf("source replaced while pinning identity: %s", source)
	}
	if image != nil {
		if image.Symlink {
			link, e := from.readLink()
			if e != nil {
				return e
			}
			if err = verifyLinkImage(link, image); err != nil {
				return err
			}
		} else if err = verifyOpenedImage(opened, image); err != nil {
			return err
		}
	}
	if err = before(); err != nil {
		return err
	}
	if err = parents(); err != nil {
		return err
	}
	if err = renameAnchoredExclusive(from.parent, from.leaf, to.parent, to.leaf); err != nil {
		return err
	}
	afterErr := after()
	if err = unix.Fstatat(to.parent, to.leaf, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return errors.Join(afterErr, err)
	}
	moved := statIdentity(&st)
	var ownership error
	if moved != identity {
		ownership = fmt.Errorf("source inode substituted in native rename window")
	}
	ownership = errors.Join(ownership, parents())
	if ownership == nil && image != nil {
		if image.Symlink {
			link, e := to.readLink()
			ownership = e
			if e == nil {
				ownership = verifyLinkImage(link, image)
			}
		} else {
			_, ownership = opened.Seek(0, 0)
			if ownership == nil {
				ownership = verifyOpenedImage(opened, image)
			}
		}
	}
	if ownership != nil {
		if publicSource {
			return restoreUnexpectedMove(from, to, source, target, moved, errors.Join(afterErr, ownership))
		}
		// Metadata sources are locally trusted. A post-publication external
		// destination/parent substitution is not authority to move that newer
		// public inode into metadata. Leave it untouched for safe abort.
		return errors.Join(afterErr, ownership)
	}
	if afterErr != nil {
		return afterErr
	}
	if err = barrier(); err != nil {
		return err
	}
	return errors.Join(unix.Fsync(from.parent), unix.Fsync(to.parent))
}

// A rename may have consumed its source before the destination-parent barrier.
// Re-pin the restored identity; never follow or replace a later public entry.
func anchoredBarrier(path, identity string, image *fileImage, check, barrier func() error) (err error) {
	a, err := anchorPath(path, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.close()) }()
	if err = check(); err != nil {
		return err
	}
	var opened *os.File
	if image == nil {
		opened, err = a.openDirectory()
	} else if image.Symlink {
		opened, err = a.openLink()
	} else {
		opened, err = a.openRead()
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, opened.Close()) }()
	owned := func() error {
		if err := check(); err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstat(int(opened.Fd()), &st); err != nil {
			return err
		}
		if statIdentity(&st) != identity {
			return fmt.Errorf("restored identity changed: %s", path)
		}
		if err := unix.Fstatat(a.parent, a.leaf, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if statIdentity(&st) != identity {
			return fmt.Errorf("restored public binding changed: %s", path)
		}
		if image != nil {
			if image.Symlink {
				link, err := a.readLink()
				if err != nil {
					return err
				}
				return verifyLinkImage(link, image)
			}
			if _, err := opened.Seek(0, 0); err != nil {
				return err
			}
			return verifyOpenedImage(opened, image)
		}
		return nil
	}
	if err = owned(); err != nil {
		return err
	}
	if err = barrier(); err != nil {
		return err
	}
	if err = owned(); err != nil {
		return err
	}
	// POSIX links have no portable fsync-able leaf descriptor. Their pinned
	// containing directory is the namespace durability boundary.
	if image == nil || !image.Symlink {
		err = opened.Sync()
	}
	return errors.Join(err, unix.Fsync(a.parent), owned())
}
