package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// ResolveRevision accepts HEAD, full typed commit IDs, exact refs, unambiguous
// short branch/tag names, and Git-like ~n/^n parent selection. No path guessing,
// abbreviated hashes, tag peeling, or reflog/range syntax is performed.
func (r *Repository) ResolveRevision(ctx context.Context, revision string) (storev2.ID, error) {
	if err := ctx.Err(); err != nil {
		return storev2.ID{}, err
	}
	base := revision
	if i := strings.IndexAny(base, "~^"); i >= 0 {
		base = base[:i]
	}
	var id storev2.ID
	var err error
	switch {
	case base == "":
		return id, fmt.Errorf("missing revision")
	case base == "HEAD":
		_, id, err = r.current()
	case strings.HasPrefix(base, "pv2:"):
		id, err = storev2.ParseID(base)
	default:
		refs, e := r.store.ListRefs(ctx)
		if e != nil {
			return id, e
		}
		names := []string{"heads/" + base, "tags/" + base}
		if strings.HasPrefix(base, "heads/") || strings.HasPrefix(base, "tags/") {
			names = []string{base}
		}
		matches := 0
		for _, name := range names {
			if target, ok := refs[name]; ok {
				id = target
				matches++
			}
		}
		if matches != 1 {
			return storev2.ID{}, fmt.Errorf("revision %q is missing, malformed or ambiguous; use an exact heads/ or tags/ ref", base)
		}
	}
	if err != nil {
		return storev2.ID{}, err
	}
	if id.Kind != storev2.Commit {
		return storev2.ID{}, fmt.Errorf("revision %q does not identify a commit", base)
	}
	if err = r.store.Verify(ctx, id); err != nil {
		return storev2.ID{}, err
	}
	suffix := revision[len(base):]
	steps := 0
	for suffix != "" {
		op := suffix[0]
		if op != '~' && op != '^' {
			return storev2.ID{}, fmt.Errorf("malformed revision %q", revision)
		}
		suffix = suffix[1:]
		n := 0
		for n < len(suffix) && suffix[n] >= '0' && suffix[n] <= '9' {
			n++
		}
		count := uint64(1)
		if n > 0 {
			count, err = strconv.ParseUint(suffix[:n], 10, 32)
			if err != nil {
				return storev2.ID{}, fmt.Errorf("invalid revision parent count: %w", err)
			}
		}
		suffix = suffix[n:]
		if count > storev2.MaxGraphObjects {
			return storev2.ID{}, storev2.ErrGraphLimit
		}
		iterations := count
		if op == '^' && count > 0 {
			iterations = 1
		}
		for i := uint64(0); i < iterations; i++ {
			steps++
			if steps > storev2.MaxGraphObjects {
				return storev2.ID{}, storev2.ErrGraphLimit
			}
			if err = ctx.Err(); err != nil {
				return storev2.ID{}, err
			}
			c, e := r.store.ReadCommit(ctx, id)
			if e != nil {
				return storev2.ID{}, e
			}
			parent := uint64(1)
			if op == '^' {
				parent = count
			}
			if parent > uint64(len(c.Parents)) {
				return storev2.ID{}, fmt.Errorf("revision %q has no requested parent", revision)
			}
			id = c.Parents[parent-1]
		}
	}
	return id, nil
}

// LogRevision follows all ordered parents from the explicitly resolved commit.
func (r *Repository) LogRevision(ctx context.Context, revision string, visit func(storev2.ID, storev2.CommitData) error) error {
	id, err := r.ResolveRevision(ctx, revision)
	if err != nil {
		return err
	}
	return r.logFrom(ctx, id, visit)
}

func (r *Repository) logFrom(ctx context.Context, id storev2.ID, visit func(storev2.ID, storev2.CommitData) error) error {
	pending := []storev2.ID{id}
	seen := map[storev2.ID]bool{}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		id = pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[id] {
			continue
		}
		if len(seen) >= storev2.MaxGraphObjects {
			return storev2.ErrGraphLimit
		}
		seen[id] = true
		c, err := r.store.ReadCommit(ctx, id)
		if err != nil {
			return err
		}
		if err = visit(id, c); err != nil {
			return err
		}
		for i := len(c.Parents) - 1; i >= 0; i-- {
			pending = append(pending, c.Parents[i])
		}
	}
	return nil
}
