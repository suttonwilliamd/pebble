package storev2

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ListRefs returns a verified snapshot of the ref catalog, sharing one bounded
// graph walk across all roots. Callers must not infer physical object locations.
func (s *Store) ListRefs(ctx context.Context) (map[string]ID, error) {
	refs, _, err := s.verifiedRefs(ctx)
	return refs, err
}
func (s *Store) VerifyRefs(ctx context.Context) (int, error) {
	_, count, err := s.verifiedRefs(ctx)
	return count, err
}
func (s *Store) verifiedRefs(ctx context.Context) (map[string]ID, int, error) {
	if err := s.guard(); err != nil {
		return nil, 0, err
	}
	refs, err := s.refs(ctx)
	if err != nil {
		return nil, 0, err
	}
	names := make(map[string]bool, len(refs))
	for name := range refs {
		key := strings.ToLower(name)
		if names[key] {
			return nil, 0, invalid("ref catalog collision")
		}
		names[key] = true
	}
	for name := range names {
		for end := strings.LastIndexByte(name, '/'); end > 0; end = strings.LastIndexByte(name[:end], '/') {
			if names[name[:end]] {
				return nil, 0, invalid("ref catalog collision")
			}
		}
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	g := s.graph(ctx, buffer[:])
	for name, id := range refs {
		if err := g.verify(id); err != nil {
			return nil, 0, err
		}
		if err := tagBinding(name, id, g); err != nil {
			return nil, 0, err
		}
	}
	return refs, len(g.nodes), nil
}

// HEAD is local symbolic metadata, not an object or a bypass around ref CAS.
func (s *Store) Head(ctx context.Context) (_ string, err error) {
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = s.guard(); err != nil {
		return "", err
	}
	f, err := regular(filepath.Join(s.meta, "HEAD"))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var b [4110]byte
	n, readErr := io.ReadFull(f, b[:])
	if readErr != io.ErrUnexpectedEOF {
		return "", invalid("HEAD length")
	}
	value := string(b[:n])
	if !strings.HasPrefix(value, "ref: heads/") || !strings.HasSuffix(value, "\n") {
		return "", invalid("HEAD")
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, "ref: "), "\n")
	if len(name) > 4096 || !validRef(name) {
		return "", invalid("HEAD")
	}
	return name, nil
}

// UpdateHead uses exact symbolic compare-and-swap and verifies the destination.
// Only an entirely unborn repository may point at an absent heads/main.
func (s *Store) UpdateHead(ctx context.Context, expected, target string) (err error) {
	if len(target) > 4096 || !strings.HasPrefix(target, "heads/") || !validRef(target) {
		return invalid("HEAD")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release(&err, unlock)
	current, err := s.Head(ctx)
	if errors.Is(err, os.ErrNotExist) {
		current, err = "", nil
	}
	if err != nil {
		return err
	}
	if current != expected {
		return ErrConflict
	}
	refs, err := s.ListRefs(ctx)
	if err != nil {
		return err
	}
	if _, ok := refs[target]; !ok && !(target == "heads/main" && len(refs) == 0) {
		return os.ErrNotExist
	}
	return s.writeFile(ctx, filepath.Join(s.meta, "HEAD"), []byte("ref: "+target+"\n"))
}
