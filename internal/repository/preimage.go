package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func preimageName(i int) string { return fmt.Sprintf("before-%06d", i) }

// A fingerprint is not a backup: an editor may replace a dirty restore source
// after intent publication but before retain. Keep an independent, durable
// preimage until the operation completes or automatic reconciliation exposes it.
func (r *Repository) copyImage(ctx context.Context, name, dir string, image *fileImage) (_ string, err error) {
	if err = ctx.Err(); err != nil {
		return "", err
	}
	p, err := r.safePath(name)
	if err != nil {
		return "", err
	}
	before, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	id, err := filesystemIdentity(p)
	if err != nil || id != image.Identity {
		return "", errors.Join(fmt.Errorf("preimage source ownership changed: %s", name), err)
	}
	f, err := os.CreateTemp(dir, "preimage-")
	if err != nil {
		return "", err
	}
	target := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(target)
		}
	}()
	if image.Symlink {
		if err = errors.Join(f.Close(), os.Remove(target)); err != nil {
			return "", err
		}
		a, e := anchorPath(p, false)
		if e != nil {
			return "", e
		}
		link, readErr := a.readLink()
		e = errors.Join(readErr, a.close())
		if e != nil {
			return "", e
		}
		if err = verifyLinkImage(link, image); err != nil {
			return "", err
		}
		if err = createLink(link.target, target, image.DirectoryLink); err != nil {
			return "", fmt.Errorf("host cannot preserve symbolic-link preimage %s: %w", name, err)
		}
	} else {
		err = r.readFile(name, func(source *os.File) error {
			opened, e := source.Stat()
			if e != nil {
				return e
			}
			if !os.SameFile(before, opened) || uint64(opened.Size()) != image.Size || opened.ModTime().UnixNano() != image.Modified || (opened.Mode()&0111 != 0) != image.Executable {
				return fmt.Errorf("preimage source metadata changed: %s", name)
			}
			n, sum, e := hashWorkingReader(io.TeeReader(source, f))
			if e != nil {
				return e
			}
			after, e := source.Stat()
			if e != nil {
				return e
			}
			if uint64(n) != image.Size || sum != image.Sum || !sameWorkingInfo(opened, after) {
				return fmt.Errorf("preimage source changed while copying: %s", name)
			}
			return f.Chmod(opened.Mode().Perm())
		})
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return "", err
		}
	}
	observed, err := inspectRecoveryImage(r, name)
	if err != nil || !equalImage(observed, image) {
		return "", errors.Join(fmt.Errorf("preimage source changed before capture completed: %s", name), err)
	}
	copy, err := inspectRecoveryImage(&Repository{root: dir}, filepath.Base(target))
	if err != nil || !sameImageContent(copy, image) {
		return "", errors.Join(fmt.Errorf("host cannot preserve preimage content/type: %s", name), err)
	}
	// Regular bytes and mode were already synced through the writable create
	// handle above; subsequent inspection is read-only. Native links still need
	// their own flush. Stage publication supplies the directory barriers.
	if err = r.boundary(ctx, "preimage-sync", name); err == nil && image.Symlink {
		err = syncOriginal(target)
	}
	if err != nil {
		return "", err
	}
	return target, nil
}

func sameImageContent(a, b *fileImage) bool {
	return a != nil && b != nil && a.Size == b.Size && a.Sum == b.Sum && a.Executable == b.Executable && a.Symlink == b.Symlink && (!measuresDirectoryLink || a.DirectoryLink == b.DirectoryLink)
}
