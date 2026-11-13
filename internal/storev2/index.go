package storev2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var emptyIndex = []byte("pebble-index 1\nempty\n")

const stagingIndexMagic = "pebble-index 1\n"

var zeroIndexID [33]byte

// IndexState pins an immutable staged tree to a symbolic HEAD and its base.
// Next is a prepared commit: after ref publication it proves index consumption.
// The caller must hold the repository worktree guard for every transition.
type IndexState struct {
	Head             string
	Base, Tree, Next ID
}

func (s *Store) StagingEnabled() (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	marker, err := s.marker()
	return marker == stagingMarker || marker == stagingFilesystemMarker, err
}
func (s *Store) requireStaging() error {
	enabled, err := s.StagingEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("staging-v1 capability required; run pebble add first")
	}
	return nil
}
func (s *Store) readIndexBytes() (_ []byte, err error) {
	f, err := regular(filepath.Join(s.meta, "index"))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err == nil && len(data) > 8192 {
		err = invalid("index length")
	}
	return data, err
}
func (s *Store) ReadIndex(ctx context.Context) (*IndexState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	enabled, err := s.StagingEnabled()
	if err != nil {
		return nil, err
	}
	data, err := s.readIndexBytes()
	if !enabled {
		if errors.Is(err, os.ErrNotExist) || err == nil && bytes.Equal(data, emptyIndex) {
			return nil, nil
		}
		return nil, errors.Join(fmt.Errorf("unexpected index without staging-v1"), err)
	}
	if err != nil {
		return nil, fmt.Errorf("staging index unavailable; preserve metadata: %w", err)
	}
	if bytes.Equal(data, emptyIndex) {
		return nil, nil
	}
	if len(data) < len(stagingIndexMagic)+4+99+32 || !bytes.HasPrefix(data, []byte(stagingIndexMagic)) {
		return nil, invalid("index envelope")
	}
	payload := data[:len(data)-32]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], data[len(data)-32:]) {
		return nil, invalid("index checksum")
	}
	d := decoder{b: payload[len(stagingIndexMagic):]}
	state := &IndexState{Head: string(d.text(false))}
	optional := func() ID {
		b := d.take(33)
		if b == nil {
			return ID{}
		}
		if bytes.Equal(b, zeroIndexID[:]) {
			return ID{}
		}
		child := decoder{b: b}
		id := child.link(Commit)
		if child.err != nil {
			d.err = child.err
		}
		return id
	}
	state.Base = optional()
	state.Tree = d.link(Tree)
	state.Next = optional()
	if d.err != nil || len(d.b) != 0 || len(state.Head) > 4096 || !ValidBranchFromHead(state.Head) {
		return nil, invalid("index fields")
	}
	if err = s.Verify(ctx, state.Tree); err != nil {
		return nil, err
	}
	if state.Base != (ID{}) {
		if err = s.Verify(ctx, state.Base); err != nil {
			return nil, err
		}
	}
	if state.Next != (ID{}) {
		c, e := s.ReadCommit(ctx, state.Next)
		if e != nil {
			return nil, e
		}
		parentsOK := state.Base == (ID{}) && len(c.Parents) == 0 || len(c.Parents) == 1 && c.Parents[0] == state.Base
		if c.Tree != state.Tree || !parentsOK {
			return nil, invalid("index prepared commit")
		}
		if err = s.Verify(ctx, state.Next); err != nil {
			return nil, err
		}
	}
	return state, nil
}
func ValidBranchFromHead(head string) bool {
	return len(head) > 6 && head[:6] == "heads/" && ValidBranch(head[6:])
}

// WriteIndex atomically replaces local metadata after closure durability. A nil
// state is a canonical neutral index, not a missing/malformed-index fallback.
func (s *Store) WriteIndex(ctx context.Context, state *IndexState) (err error) {
	if err = s.requireStaging(); err != nil {
		return err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release(&err, unlock)
	data := emptyIndex
	if state != nil {
		if !ValidBranchFromHead(state.Head) || state.Tree.Kind != Tree || state.Base != (ID{}) && state.Base.Kind != Commit || state.Next != (ID{}) && state.Next.Kind != Commit {
			return invalid("index intent")
		}
		head, e := s.Head(ctx)
		if e != nil {
			return e
		}
		if head != state.Head {
			return ErrConflict
		}
		refs, e := s.refs(ctx)
		if e != nil {
			return e
		}
		current := refs[head]
		if current != state.Base && (state.Next == (ID{}) || current != state.Next) {
			return ErrConflict
		}
		buffer := buffers.Get().(*[32 << 10]byte)
		defer buffers.Put(buffer)
		g := s.graph(ctx, buffer[:])
		for _, id := range []ID{state.Tree, state.Base, state.Next} {
			if id != (ID{}) {
				if err = g.verify(id); err != nil {
					return err
				}
			}
		}
		if state.Next != (ID{}) {
			c, e := s.ReadCommit(ctx, state.Next)
			if e != nil {
				return e
			}
			if c.Tree != state.Tree || !(state.Base == (ID{}) && len(c.Parents) == 0 || len(c.Parents) == 1 && c.Parents[0] == state.Base) {
				return invalid("index prepared commit")
			}
		}
		if err = g.makeDurable(); err != nil {
			return err
		}
		data = appendText([]byte(stagingIndexMagic), state.Head)
		for _, id := range []ID{state.Base, state.Tree, state.Next} {
			data = append(data, byte(id.Kind))
			data = append(data, id.Digest[:]...)
		}
		sum := sha256.Sum256(data)
		data = append(data, sum[:]...)
	}
	return s.writeFile(ctx, filepath.Join(s.meta, "index"), data)
}
