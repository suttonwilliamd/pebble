package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// DiffOptions selects index/worktree (default), revision/worktree (one revision),
// revision/revision (two), or revision/index (Staged; defaults to HEAD).
// Paths are literal repository-relative file/subtree selectors, not revisions.
type DiffOptions struct {
	Revisions []string
	Staged    bool
	Paths     []string
}

type inspectionEntry struct {
	entry storev2.TreeEntry
	live  *workingFile
}

type inspectionFile struct {
	*os.File
	size  uint64
	sum   [32]byte
	lines uint64
	text  bool
}

func (f *inspectionFile) dispose() {
	if f != nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
}
func (f *inspectionFile) rewind() error { _, err := f.Seek(0, io.SeekStart); return err }

func inspectionEntries(entries []storev2.TreeEntry) map[string]inspectionEntry {
	out := make(map[string]inspectionEntry, len(entries))
	for _, e := range entries {
		out[e.Path] = inspectionEntry{entry: e}
	}
	return out
}

func (r *Repository) inspectionTree(ctx context.Context, revision string) ([]storev2.TreeEntry, error) {
	id, err := r.ResolveRevision(ctx, revision)
	if err != nil {
		return nil, err
	}
	return r.tree(id)
}

func (r *Repository) inspectFile(ctx context.Context, value inspectionEntry) (_ *inspectionFile, err error) {
	file, err := os.CreateTemp("", "pebble-inspect-*")
	if err != nil {
		return nil, err
	}
	result := &inspectionFile{File: file, text: true}
	defer func() {
		if err != nil {
			result.dispose()
		}
	}()
	h := sha256.New()
	w := io.MultiWriter(file, h)
	if value.live == nil {
		err = r.store.CopyFile(ctx, value.entry.Manifest, w)
	} else if value.live.symlink {
		var p string
		p, err = r.safePath(value.entry.Path)
		if err == nil {
			var link linkContent
			link, err = readLink(p)
			if err == nil {
				_, err = io.WriteString(w, link.target)
			}
		}
	} else {
		err = r.readFile(value.entry.Path, func(f *os.File) error {
			info, e := f.Stat()
			if e != nil {
				return e
			}
			if !sameWorkingInfo(value.live.info, info) {
				return fmt.Errorf("file changed before inspection: %s", value.entry.Path)
			}
			_, e = io.Copy(w, &inspectionReader{ctx: ctx, r: f})
			return e
		})
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	result.size = uint64(info.Size())
	copy(result.sum[:], h.Sum(nil))
	if value.live != nil {
		actual, e := r.hashFile(value.entry.Path)
		if e != nil {
			return nil, e
		}
		if !sameWorkingInfo(value.live.info, actual.info) || result.size != value.live.size || result.sum != value.live.sum || actual.sum != result.sum || measuresExecutable && actual.executable != value.live.executable || actual.symlink != value.live.symlink || measuresDirectoryLink && actual.directoryLink != value.live.directoryLink {
			return nil, fmt.Errorf("file changed during inspection: %s", value.entry.Path)
		}
	}
	if err = result.rewind(); err != nil {
		return nil, err
	}
	// Rune decoding uses a fixed-size buffered reader; no whole-file/line limit.
	reader := bufio.NewReader(&inspectionReader{ctx: ctx, r: file})
	last := rune('\n')
	for {
		ch, size, e := reader.ReadRune()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if ch == 0 || ch == utf8.RuneError && size == 1 {
			result.text = false
		}
		if ch == '\n' {
			result.lines++
		}
		last = ch
	}
	if result.size > 0 && last != '\n' {
		result.lines++
	}
	if err = result.rewind(); err != nil {
		return nil, err
	}
	return result, nil
}

type inspectionReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *inspectionReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// ShowFile emits verified literal bytes, including binary bytes and link targets.
// It neither follows worktree links nor materializes a stored file in the tree.
func (r *Repository) ShowFile(ctx context.Context, revision, name string, w io.Writer) error {
	if err := validPath(name); err != nil {
		return err
	}
	entries, err := r.inspectionTree(ctx, revision)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Path != name {
			continue
		}
		file, err := r.inspectFile(ctx, inspectionEntry{entry: e})
		if err != nil {
			return err
		}
		defer file.dispose()
		_, err = io.Copy(w, &inspectionReader{ctx: ctx, r: file})
		return err
	}
	return fmt.Errorf("path not present in revision %q: %s", revision, name)
}

// Diff is read-only even with a pending journal. It never acquires/creates a
// writer guard, changes the index, ingests worktree bytes or resumes recovery.
func (r *Repository) Diff(ctx context.Context, options DiffOptions, w io.Writer) error {
	if len(options.Revisions) > 2 || options.Staged && len(options.Revisions) > 1 {
		return fmt.Errorf("diff accepts at most two revisions (one with --staged)")
	}
	for _, path := range options.Paths {
		if path != "." {
			if err := validPath(path); err != nil {
				return err
			}
		}
	}
	var before, after []storev2.TreeEntry
	var err error
	if len(options.Revisions) > 0 {
		before, err = r.inspectionTree(ctx, options.Revisions[0])
	} else if options.Staged {
		before, err = r.TreeEntries()
	} else {
		_, before, err = r.index()
	}
	if err != nil {
		return err
	}
	working := !options.Staged && len(options.Revisions) < 2
	if len(options.Revisions) == 2 {
		after, err = r.inspectionTree(ctx, options.Revisions[1])
	} else {
		_, after, err = r.index()
	}
	if err != nil {
		return err
	}
	old, target := inspectionEntries(before), inspectionEntries(after)
	if working {
		// Include revision-tracked and index-tracked paths, but never unrelated
		// untracked files. scanSelected safely handles file/directory transitions.
		known := inspectionEntries(before)
		for name, e := range target {
			known[name] = e
		}
		tracked := make([]storev2.TreeEntry, 0, len(known))
		for _, e := range known {
			tracked = append(tracked, e.entry)
		}
		paths := options.Paths
		if len(paths) == 0 {
			paths = []string{"."}
		}
		files, e := r.scanSelected(tracked, paths, true)
		if e != nil {
			return e
		}
		target = map[string]inspectionEntry{}
		for i := range files {
			f := &files[i]
			target[f.name] = inspectionEntry{entry: storev2.TreeEntry{Path: f.name, Executable: f.executable, Symlink: f.symlink, DirectoryLink: f.directoryLink}, live: f}
		}
	}
	names := map[string]bool{}
	for name := range old {
		names[name] = true
	}
	for name := range target {
		names[name] = true
	}
	for _, path := range options.Paths {
		found := false
		for name := range names {
			found = found || selectedPath(name, []string{path})
		}
		if !found {
			return fmt.Errorf("path does not match either diff side: %s", path)
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		if len(options.Paths) == 0 || selectedPath(name, options.Paths) {
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		if err = ctx.Err(); err != nil {
			return err
		}
		a, aok := old[name]
		b, bok := target[name]
		if aok && bok && a.live == nil && b.live == nil && a.entry == b.entry {
			continue
		}
		if err = r.diffFile(ctx, name, a, aok, b, bok, w); err != nil {
			return err
		}
	}
	return nil
}

// DiffCommit compares one commit with its first parent, or the empty tree.
func (r *Repository) DiffCommit(ctx context.Context, revision string, w io.Writer) error {
	id, err := r.ResolveRevision(ctx, revision)
	if err != nil {
		return err
	}
	c, err := r.store.ReadCommit(ctx, id)
	if err != nil {
		return err
	}
	if len(c.Parents) > 0 {
		return r.Diff(ctx, DiffOptions{Revisions: []string{c.Parents[0].String(), id.String()}}, w)
	}
	entries, err := r.tree(id)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err = r.diffFile(ctx, e.Path, inspectionEntry{}, false, inspectionEntry{entry: e}, true, w); err != nil {
			return err
		}
	}
	return nil
}

func entryMode(e inspectionEntry, present bool) string {
	if !present {
		return "absent"
	}
	if e.entry.Symlink {
		if e.entry.DirectoryLink {
			return "directory-link"
		}
		return "symlink"
	}
	if e.entry.Executable {
		return "executable"
	}
	return "regular"
}
func (r *Repository) diffFile(ctx context.Context, name string, a inspectionEntry, aok bool, b inspectionEntry, bok bool, w io.Writer) error {
	var old, next *inspectionFile
	var err error
	if aok {
		old, err = r.inspectFile(ctx, a)
		if err != nil {
			return err
		}
		defer old.dispose()
	}
	if bok {
		next, err = r.inspectFile(ctx, b)
		if err != nil {
			return err
		}
		defer next.dispose()
	}
	am, bm := entryMode(a, aok), entryMode(b, bok)
	if old != nil && next != nil && old.size == next.size && old.sum == next.sum && am == bm {
		return nil
	}
	if _, err = fmt.Fprintf(w, "diff --pebble %q\nold mode %s\nnew mode %s\n", name, am, bm); err != nil {
		return err
	}
	if old != nil && !old.text || next != nil && !next.text {
		return binaryDiff(ctx, old, next, w)
	}
	al, bl := "/dev/null", "/dev/null"
	if aok {
		al = "a/" + name
	}
	if bok {
		bl = "b/" + name
	}
	var ac, bc uint64
	if old != nil {
		ac = old.lines
	}
	if next != nil {
		bc = next.lines
	}
	as, bs := 1, 1
	if ac == 0 {
		as = 0
	}
	if bc == 0 {
		bs = 0
	}
	if _, err = fmt.Fprintf(w, "--- %q\n+++ %q\n@@ -%d,%d +%d,%d @@\n", al, bl, as, ac, bs, bc); err != nil {
		return err
	}
	if err = writeTextSide(ctx, old, '-', w); err != nil {
		return err
	}
	return writeTextSide(ctx, next, '+', w)
}

func writeTextSide(ctx context.Context, file *inspectionFile, prefix byte, w io.Writer) error {
	if file == nil {
		return nil
	}
	reader := bufio.NewReader(&inspectionReader{ctx: ctx, r: file})
	start := true
	marker := [1]byte{prefix}
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > 0 {
			if start {
				if _, e := w.Write(marker[:]); e != nil {
					return e
				}
			}
			if _, e := w.Write(part); e != nil {
				return e
			}
			start = part[len(part)-1] == '\n'
		}
		if err == io.EOF {
			if !start {
				_, err = io.WriteString(w, "\n\\ No newline at end of file\n")
				return err
			}
			return nil
		}
		if err != nil && err != bufio.ErrBufferFull {
			return err
		}
	}
}

func binaryDiff(ctx context.Context, old, next *inspectionFile, w io.Writer) error {
	for _, side := range []struct {
		name string
		file *inspectionFile
	}{{"old", old}, {"new", next}} {
		var err error
		if side.file == nil {
			_, err = fmt.Fprintf(w, "%s binary absent\n", side.name)
		} else {
			_, err = fmt.Fprintf(w, "%s binary size=%d sha256=%x\n", side.name, side.file.size, side.file.sum)
		}
		if err != nil {
			return err
		}
	}
	// Regions are maximal unequal runs at identical zero-based byte offsets,
	// followed by an unmatched tail. They are not an alignment/edit script.
	var a, b [32 << 10]byte
	var offset, start uint64
	active := false
	emit := func(end uint64) error {
		if !active {
			return nil
		}
		_, err := fmt.Fprintf(w, "region old offset=%d length=%d new offset=%d length=%d\n", start, end-start, start, end-start)
		active = false
		return err
	}
	var common, osize, nsize uint64
	if old != nil {
		osize = old.size
	}
	if next != nil {
		nsize = next.size
	}
	common = osize
	if nsize < common {
		common = nsize
	}
	for offset < common {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := uint64(len(a))
		if common-offset < n {
			n = common - offset
		}
		if _, err := io.ReadFull(old, a[:n]); err != nil {
			return err
		}
		if _, err := io.ReadFull(next, b[:n]); err != nil {
			return err
		}
		if bytes.Equal(a[:n], b[:n]) && !active {
			offset += n
			continue
		}
		for i := uint64(0); i < n; i++ {
			if a[i] != b[i] {
				if !active {
					start = offset + i
					active = true
				}
			} else if err := emit(offset + i); err != nil {
				return err
			}
		}
		offset += n
	}
	if err := emit(common); err != nil {
		return err
	}
	if osize != nsize {
		_, err := fmt.Fprintf(w, "region old offset=%d length=%d new offset=%d length=%d\n", common, osize-common, common, nsize-common)
		return err
	}
	return nil
}
