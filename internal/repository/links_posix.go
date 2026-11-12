//go:build !windows

package repository

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

const measuresExecutable = true
const measuresDirectoryLink = false

func (a *pathAnchor) readLink() (linkContent, error) {
	buffer := worktreeBuffers.Get().(*[maxLinkBytes + 1]byte)
	defer worktreeBuffers.Put(buffer)
	b := buffer[:]
	n, err := unix.Readlinkat(a.parent, a.leaf, b)
	if err != nil {
		return linkContent{}, err
	}
	if n > maxLinkBytes {
		return linkContent{}, fmt.Errorf("symbolic-link target too long")
	}
	return linkContent{target: string(b[:n])}, nil
}

// Without an origin tag, backslash/drive spellings cannot be distinguished
// from Windows path semantics. Preserve such wire graphs, refuse host mutation.
func validateMaterializableLink(target string) error {
	if err := validLinkTarget(target); err != nil {
		return err
	}
	drive := len(target) >= 2 && target[1] == ':' && ((target[0] >= 'A' && target[0] <= 'Z') || (target[0] >= 'a' && target[0] <= 'z'))
	if drive || strings.ContainsRune(target, '\\') {
		return fmt.Errorf("unsupported ambiguous Windows/POSIX symbolic-link target %q; history preserved", target)
	}
	return nil
}
func createLink(target, path string, _ bool) error { return os.Symlink(target, path) }
