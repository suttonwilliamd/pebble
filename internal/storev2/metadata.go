package storev2

import (
	"bytes"
	"context"
	"encoding/binary"
)

// TreeEntry binds a portable relative path to one immutable file revision.
// Entries are supplied in canonical path order; constructors never reorder them.
type TreeEntry struct {
	Path          string
	Executable    bool
	Symlink       bool
	DirectoryLink bool
	Manifest      ID
}

// CommitData preserves parent order and every frozen metadata field, including
// signed/fractional seconds and the exact timezone spelling (including -0000).
type CommitData struct {
	Tree          ID
	Parents       []ID
	Author, Email string
	Seconds       int64
	Nanoseconds   uint32
	Offset        string
	Message       string
}

func appendText(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}
func appendLink(b []byte, id ID) []byte {
	b = append(b, byte(id.Kind))
	return append(b, id.Digest[:]...)
}

func (s *Store) CreateTree(ctx context.Context, entries []TreeEntry) (ID, error) {
	if len(entries) > 10000 {
		return ID{}, invalid("limit")
	}
	size := 4
	hasLinks := false
	for _, e := range entries {
		if e.DirectoryLink && !e.Symlink || e.Executable && e.Symlink {
			return ID{}, invalid("mode")
		}
		hasLinks = hasLinks || e.Symlink
	}
	if hasLinks {
		if err := s.requireFilesystem(); err != nil {
			return ID{}, err
		}
	}
	for _, e := range entries {
		if len(e.Path) > 4096 || size > MaxMetadataBytes-38-len(e.Path) {
			return ID{}, invalid("limit")
		}
		size += 38 + len(e.Path)
	}
	b := make([]byte, 0, size)
	b = binary.BigEndian.AppendUint32(b, uint32(len(entries)))
	for _, e := range entries {
		b = appendText(b, e.Path)
		mode := byte(0)
		if e.Executable {
			mode = 1
		}
		if e.Symlink {
			mode = 2
			if e.DirectoryLink {
				mode = 3
			}
		}
		b = append(b, mode)
		b = appendLink(b, e.Manifest)
	}
	return s.putPayload(ctx, Tree, b)
}

func (s *Store) CreateCommit(ctx context.Context, c CommitData) (ID, error) {
	if len(c.Parents) > 64 || len(c.Author) > 65535 || len(c.Email) > 65535 || len(c.Message) > 65535 || len(c.Offset) != 5 {
		return ID{}, invalid("limit")
	}
	b := make([]byte, 0, 66+33*len(c.Parents)+len(c.Author)+len(c.Email)+len(c.Message))
	b = appendLink(b, c.Tree)
	b = binary.BigEndian.AppendUint32(b, uint32(len(c.Parents)))
	for _, parent := range c.Parents {
		b = appendLink(b, parent)
	}
	b = appendText(b, c.Author)
	b = appendText(b, c.Email)
	b = binary.BigEndian.AppendUint64(b, uint64(c.Seconds))
	b = binary.BigEndian.AppendUint32(b, c.Nanoseconds)
	b = append(b, c.Offset...)
	b = appendText(b, c.Message)
	return s.putPayload(ctx, Commit, b)
}

// metadata uses the same verified loader as Copy, including packed backends.
// Its bounded result is not exposed until full address/schema verification.
func (s *Store) metadata(ctx context.Context, id ID, kind Kind) ([]byte, error) {
	if id.Kind != kind {
		return nil, invalid("id-type")
	}
	var b bytes.Buffer
	if err := s.Copy(ctx, id, &b); err != nil {
		return nil, err
	}
	return b.Bytes()[HeaderSize:], nil
}
func (s *Store) ReadTree(ctx context.Context, id ID) ([]TreeEntry, error) {
	b, err := s.metadata(ctx, id, Tree)
	if err != nil {
		return nil, err
	}
	d := decoder{b: b}
	entries := make([]TreeEntry, d.count(10000))
	for i := range entries {
		path, mode := string(d.text(false)), d.u8()
		entries[i] = TreeEntry{Path: path, Executable: mode == 1, Symlink: mode >= 2, DirectoryLink: mode == 3, Manifest: d.link(Manifest)}
	}
	return entries, d.err
}
func (s *Store) ReadCommit(ctx context.Context, id ID) (CommitData, error) {
	b, err := s.metadata(ctx, id, Commit)
	if err != nil {
		return CommitData{}, err
	}
	d := decoder{b: b}
	c := CommitData{Tree: d.link(Tree)}
	c.Parents = make([]ID, d.count(64))
	for i := range c.Parents {
		c.Parents[i] = d.link(Commit)
	}
	c.Author, c.Email = string(d.text(false)), string(d.text(false))
	c.Seconds, c.Nanoseconds = int64(d.u64()), d.u32()
	c.Offset = string(d.take(5))
	c.Message = string(d.text(true))
	return c, d.err
}

// FileInfo returns verified manifest length and raw whole-file SHA-256 metadata.
// CopyFile/Verify still validate reconstruction before bytes can be accepted.
func (s *Store) FileInfo(ctx context.Context, id ID) (uint64, [32]byte, error) {
	b, err := s.metadata(ctx, id, Manifest)
	if err != nil {
		return 0, [32]byte{}, err
	}
	d := decoder{b: b}
	length := d.u64()
	var sum [32]byte
	copy(sum[:], d.take(32))
	return length, sum, d.err
}

// ValidBranch reports whether a branch name obeys the frozen ref grammar.
func ValidBranch(name string) bool { return len(name) <= 4096-len("heads/") && validRef("heads/"+name) }
