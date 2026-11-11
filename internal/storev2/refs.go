package storev2

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pe "github.com/suttonwilliamd/pebble/internal/errors"
)

func refFilename(name string) string        { return fmt.Sprintf("%x.ref", sha256.Sum256([]byte(name))) }
func (s *Store) refPath(name string) string { return filepath.Join(s.meta, "refs", refFilename(name)) }

// Ref metadata is local, not a stored object or protocol message:
// u32 name byte length || ASCII ref name || 33-byte typed target link.
func readRef(path string) (_ string, _ ID, err error) {
	f, err := regular(path)
	if err != nil {
		return "", ID{}, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var data [4 + 4096 + 33 + 1]byte
	n, readErr := io.ReadFull(f, data[:])
	if readErr != io.ErrUnexpectedEOF || n < 4+33 {
		return "", ID{}, invalid("ref metadata length")
	}
	length := binary.BigEndian.Uint32(data[:4])
	if length > 4096 || uint64(n) != 4+uint64(length)+33 {
		return "", ID{}, invalid("ref metadata length")
	}
	name := string(data[4 : 4+int(length)])
	if !validRef(name) || filepath.Base(path) != refFilename(name) {
		return "", ID{}, invalid("ref name/address")
	}
	d := decoder{b: data[4+int(length) : n]}
	id := d.link(0)
	if d.err != nil {
		return "", ID{}, d.err
	}
	if strings.HasPrefix(name, "heads/") && id.Kind != Commit {
		return "", ID{}, invalid("branch target kind")
	}
	return name, id, nil
}

func (s *Store) refs(ctx context.Context) (_ map[string]ID, err error) {
	dir, err := os.Open(filepath.Join(s.meta, "refs"))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	refs := make(map[string]ID)
	count := 0
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		for _, entry := range entries {
			count++
			if count > MaxGraphObjects {
				return nil, ErrGraphLimit
			}
			if strings.HasPrefix(entry.Name(), tempPrefix) && entry.Type().IsRegular() {
				continue
			}
			name, id, e := readRef(filepath.Join(s.meta, "refs", entry.Name()))
			if e != nil {
				return nil, pe.Wrap(pe.ErrCodeObjectCorrupted, "v2 ref catalog "+entry.Name(), e)
			}
			refs[name] = id
		}
		if readErr == io.EOF {
			break
		}
	}
	return refs, nil
}

// GetRef returns a ref only after verifying its complete target graph.
func (s *Store) GetRef(ctx context.Context, name string) (ID, error) {
	if !validRef(name) {
		return ID{}, pe.InvalidInput("invalid v2 ref name")
	}
	if err := s.guard(); err != nil {
		return ID{}, err
	}
	_, id, err := readRef(s.refPath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ID{}, pe.Wrap(pe.ErrCodeRefNotFound, "v2 ref "+name, err)
		}
		return ID{}, pe.Wrap(pe.ErrCodeObjectCorrupted, "v2 ref "+name, err)
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	g := s.graph(ctx, buffer[:])
	if err = g.verify(id); err != nil {
		return ID{}, err
	}
	if err = tagBinding(name, id, g); err != nil {
		return ID{}, err
	}
	return id, nil
}
func tagBinding(name string, target ID, g *graph) error {
	if strings.HasPrefix(name, "tags/") && target.Kind == Tag && g.nodes[target].tagName != strings.TrimPrefix(name, "tags/") {
		return pe.Wrap(pe.ErrCodeInvalidInput, "v2 annotated tag name mismatch", ErrConflict)
	}
	return nil
}

// UpdateRef publishes a complete graph with expected-old compare-and-swap.
// nil expected means absent. Tags are create-only; no delete/force operation is
// available. Identical retries reverify both graph and catalog before success.
func (s *Store) UpdateRef(ctx context.Context, name string, expected *ID, target ID) (err error) {
	if !validRef(name) || !target.Kind.valid() || expected != nil && !expected.Kind.valid() {
		return pe.InvalidInput("invalid v2 ref intent")
	}
	isTag := strings.HasPrefix(name, "tags/")
	if !isTag && (target.Kind != Commit || expected != nil && expected.Kind != Commit) {
		return pe.InvalidInput("v2 branch requires commit IDs")
	}
	if isTag && expected != nil {
		return pe.Wrap(pe.ErrCodeRefConflict, "v2 tags are create-only", ErrConflict)
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release(&err, unlock)
	refs, err := s.refs(ctx)
	if err != nil {
		return err
	}
	key := strings.ToLower(name)
	for existing := range refs {
		other := strings.ToLower(existing)
		if existing != name && (other == key || strings.HasPrefix(other, key+"/") || strings.HasPrefix(key, other+"/")) {
			return pe.Wrap(pe.ErrCodeRefConflict, "v2 ref name collision with "+existing, ErrConflict)
		}
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	g := s.graph(ctx, buffer[:])
	if err = g.verify(target); err != nil {
		return err
	}
	if err = tagBinding(name, target, g); err != nil {
		return err
	}
	current, exists := refs[name]
	if exists {
		if err = g.verify(current); err != nil {
			return err
		}
		if err = tagBinding(name, current, g); err != nil {
			return err
		}
	}
	if !exists {
		if expected != nil {
			return pe.Wrap(pe.ErrCodeRefConflict, "v2 expected ref is absent", ErrConflict)
		}
	} else if current != target {
		if expected == nil || current != *expected || isTag || !g.ancestor(current, target) {
			return pe.Wrap(pe.ErrCodeRefConflict, "v2 stale or non-fast-forward ref update", ErrConflict)
		}
	}
	// Republish the same complete metadata on retry to establish a new durable
	// namespace barrier after an earlier uncertain publication result.
	if err = g.makeDurable(); err != nil {
		return err
	}
	data := make([]byte, 4+len(name)+33)
	binary.BigEndian.PutUint32(data[:4], uint32(len(name)))
	copy(data[4:], name)
	data[4+len(name)] = byte(target.Kind)
	copy(data[5+len(name):], target.Digest[:])
	return s.writeFile(ctx, s.refPath(name), data)
}
