package storev2

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Inventory returns sorted unique typed IDs, validating every published physical
// copy, including unreachable objects and duplicate loose/packed representations.
// It is read-only: damaged derived indexes are reconstructed only in memory.
// Concurrent cooperating writes only add immutable objects; this is not a
// transactionally frozen snapshot of concurrent additions.
func (s *Store) Inventory(ctx context.Context) (_ []ID, err error) {
	if err = s.guard(); err != nil {
		return nil, err
	}
	seen := make(map[ID]struct{})
	add := func(id ID) error {
		seen[id] = struct{}{}
		if len(seen) > MaxGraphObjects {
			return ErrGraphLimit
		}
		return nil
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	dir, err := os.Open(filepath.Join(s.meta, "objects"))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	count := 0
	for {
		entries, e := dir.ReadDir(128)
		if e != nil && e != io.EOF {
			return nil, e
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			count++
			if count > MaxGraphObjects {
				return nil, ErrGraphLimit
			}
			name := entry.Name()
			if strings.HasPrefix(name, tempPrefix) && entry.Type().IsRegular() {
				continue
			}
			if len(name) != 71 || name[2] != '-' || name[67:] != ".obj" {
				return nil, corrupt(ID{}, invalid("loose filename"))
			}
			k, e := hex.DecodeString(name[:2])
			if e != nil {
				return nil, e
			}
			digest, e := hex.DecodeString(name[3:67])
			if e != nil {
				return nil, e
			}
			id := ID{Kind: Kind(k[0])}
			copy(id.Digest[:], digest)
			if !id.Kind.valid() || filepath.Base(s.objectPath(id)) != name {
				return nil, corrupt(id, invalid("loose filename/address"))
			}
			f, e := regular(s.objectPath(id))
			if e != nil {
				return nil, corrupt(id, e)
			}
			_, e = inspect(ctx, id, f, nil, nil, buffer[:], nil)
			e = errors.Join(e, f.Close())
			if e != nil {
				return nil, corrupt(id, e)
			}
			if err = add(id); err != nil {
				return nil, err
			}
		}
		if e == io.EOF {
			break
		}
	}
	names, err := s.packNames(ctx, true)
	if err != nil {
		return nil, packError(err)
	}
	total := 0
	for _, name := range names {
		p, e := s.scanPack(ctx, name, true, buffer[:])
		if e != nil {
			return nil, packError(e)
		}
		total += len(p.entries)
		if total > MaxPackObjects {
			return nil, ErrGraphLimit
		}
		for _, e := range p.entries {
			if err = add(e.id); err != nil {
				return nil, err
			}
		}
	}
	ids := make([]ID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return idLess(ids[i], ids[j]) })
	return ids, nil
}
