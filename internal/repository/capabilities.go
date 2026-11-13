package repository

import "context"

// EnableFilesystem explicitly opts this repository into literal-link entries.
// The store owns the shared worktree/store lock protocol and durable marker.
func (r *Repository) EnableFilesystem(ctx context.Context) error {
	return r.store.EnableFilesystem(ctx)
}
