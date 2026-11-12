package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// MigrationOptions selects branch suffixes (without heads/). Empty Branches
// selects all branches. Head defaults to the source HEAD, which must be selected.
// Only the single unborn heads/main layout is representable without commits.
type MigrationOptions struct {
	Branches []string
	Head     string
}

// MigrationResult is also saved as .pebble/migration-v1.json in the destination.
// Objects maps every selected reachable v1 blob/tree/commit to its typed v2 ID.
// An empty branch ID denotes the single supported unborn main branch.
type MigrationResult struct {
	Head     string            `json:"head"`
	Branches map[string]string `json:"branches"`
	Objects  map[string]string `json:"objects"`
}

type v1Migration struct {
	source      *v1Source
	destination *Repository
	ids         map[string]storev2.ID
	active      map[string]bool
	metadata    int
}

// MigrateV1 explicitly copies a quiescent, exact v1 root into a NEW separate
// destination. Source access is read-only, even on discovery/error paths. All
// selected ancestors and file revisions are verified before no-replace
// publication. Interrupted private .pebble-migrate-* stages are never success;
// an OS durability error may leave a complete destination with an uncertain
// publication error. Never retry into or automatically remove that destination.
func MigrateV1(ctx context.Context, source, destination string, options MigrationOptions) (*MigrationResult, error) {
	return migrateV1(ctx, source, destination, options, nil)
}

// beforePublish is an internal failure seam, not a public execution hook.
func migrateV1(ctx context.Context, source, destination string, options MigrationOptions, beforePublish func() error) (out *MigrationResult, err error) {
	source, err = exactDirectory(source)
	if err != nil {
		return nil, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	parent, err := exactDirectory(filepath.Dir(destination))
	if err != nil {
		return nil, err
	}
	if err = validPath(filepath.Base(destination)); err != nil {
		return nil, err
	}
	if insidePath(source, destination) || insidePath(destination, source) {
		return nil, fmt.Errorf("source/destination overlap")
	}
	// SameFile also catches mount/short-name aliases not visible lexically.
	srcInfo, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	for p := parent; ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if os.SameFile(srcInfo, info) {
			return nil, fmt.Errorf("source/destination alias overlap")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err = absentDestination(destination); err != nil {
		return nil, err
	}
	s := &v1Source{root: source, reads: map[string][32]byte{}, budget: storev2.MaxGraphReadBytes}
	refs, head, err := s.refs(ctx)
	if err != nil {
		return nil, err
	}
	sourceHead := head
	selected := map[string]string{}
	if len(options.Branches) == 0 {
		for name, hash := range refs {
			selected[name] = hash
		}
	} else {
		for _, branch := range options.Branches {
			if !storev2.ValidBranch(branch) {
				return nil, fmt.Errorf("invalid selected branch %q", branch)
			}
			name := "heads/" + branch
			hash, ok := refs[name]
			if !ok {
				return nil, fmt.Errorf("selected branch missing: %s", branch)
			}
			if _, exists := selected[name]; exists {
				return nil, fmt.Errorf("duplicate selected branch: %s", branch)
			}
			selected[name] = hash
		}
	}
	if options.Head != "" {
		if !storev2.ValidBranch(options.Head) {
			return nil, fmt.Errorf("invalid selected HEAD")
		}
		head = "heads/" + options.Head
	}
	if _, ok := selected[head]; !ok {
		return nil, fmt.Errorf("source HEAD not selected; explicitly select destination Head")
	}
	unborn := len(selected) == 1 && head == "heads/main" && selected[head] == ""
	for name, hash := range selected {
		if hash == "" && !unborn {
			return nil, fmt.Errorf("unrepresentable unborn branch %s", name)
		}
	}
	stage, err := os.MkdirTemp(parent, ".pebble-migrate-")
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(stage))
		if err != nil {
			out = nil
		}
	}()
	root := filepath.Join(stage, "repository")
	if err = os.Mkdir(root, 0700); err != nil {
		return nil, err
	}
	repo, err := Init(root)
	if err != nil {
		return nil, err
	}
	m := &v1Migration{s, repo, map[string]storev2.ID{}, map[string]bool{}, 0}
	result := &MigrationResult{Head: head, Branches: map[string]string{}, Objects: map[string]string{}}
	for _, name := range sortedV1Refs(selected) {
		hash := selected[name]
		if hash == "" {
			result.Branches[name] = ""
			continue
		}
		id, err := m.commit(ctx, hash, 0)
		if err != nil {
			return nil, fmt.Errorf("branch %s: %w", name, err)
		}
		if err = repo.store.UpdateRef(ctx, name, nil, id); err != nil {
			return nil, err
		}
		result.Branches[name] = id.String()
	}
	if err = repo.store.UpdateHead(ctx, "heads/main", head); err != nil {
		return nil, err
	}
	// Independently reopen and compare all mapped revisions and graph edges,
	// rather than trusting the IDs returned by constructors or just current HEAD.
	repo, err = Open(root)
	if err != nil {
		return nil, err
	}
	m.destination = repo
	for hash, id := range m.ids {
		if err = m.verify(ctx, hash, id); err != nil {
			return nil, fmt.Errorf("verify mapping %s: %w", hash, err)
		}
		result.Objects[hash] = id.String()
	}
	actual, err := repo.store.ListRefs(ctx)
	if err != nil {
		return nil, err
	}
	for name, hash := range selected {
		if hash == "" {
			continue
		}
		if actual[name] != m.ids[hash] {
			return nil, fmt.Errorf("branch mapping mismatch: %s", name)
		}
	}
	expectedRefs := len(selected)
	if unborn {
		expectedRefs = 0
	}
	if len(actual) != expectedRefs {
		return nil, fmt.Errorf("destination ref catalog mismatch")
	}
	gotHead, err := repo.GetHead()
	if err != nil {
		return nil, err
	}
	if gotHead != head {
		return nil, fmt.Errorf("destination HEAD mismatch")
	}
	// Restore HEAD into the private empty tree, with the repository's path guards
	// and verified streaming writer. Historical trees were separately path-probed.
	entries, err := repo.TreeEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		path, err := repo.safePath(entry.Path)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		temporary, err := repo.stageFile(root, entry)
		if err != nil {
			return nil, err
		}
		if err = os.Rename(temporary, path); err != nil {
			return nil, err
		}
		matches, err := repo.matches(entry)
		if err != nil {
			return nil, err
		}
		if !matches {
			return nil, fmt.Errorf("restored HEAD metadata mismatch: %s", entry.Path)
		}
	}
	receipt, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(root, ".pebble", "migration-v1.json"), append(receipt, '\n'), 0600); err != nil {
		return nil, err
	}
	if beforePublish != nil {
		if err = beforePublish(); err != nil {
			return nil, err
		}
	}
	// Re-read every consumed source file and the full ref catalog immediately
	// before publication. This is detection, not a lock on external writers.
	for relative := range s.reads {
		limit := int64(MaxV1EnvelopeBytes)
		if !strings.HasPrefix(relative, ".pebble/objects/") {
			limit = 8192
		}
		if _, err = s.read(ctx, relative, limit); err != nil {
			return nil, err
		}
	}
	finalRefs, finalHead, err := s.refs(ctx)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(refs, finalRefs) || finalHead != sourceHead {
		return nil, fmt.Errorf("source ref catalog changed")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = absentDestination(destination); err != nil {
		return nil, err
	}
	if _, err = exactDirectory(parent); err != nil {
		return nil, err
	}
	if err = publishMigration(root, destination); err != nil {
		return nil, fmt.Errorf("migration publication failed (inspect destination before retry): %w", err)
	}
	return result, nil
}

func insidePath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func absentDestination(path string) error {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		if err == nil {
			err = os.ErrExist
		}
		return fmt.Errorf("destination must not exist: %w", err)
	}
	return nil
}

func (m *v1Migration) commitData(ctx context.Context, hash string) (snapshot.Commit, error) {
	obj, err := m.source.object(ctx, hash, snapshot.ObjectTypeCommit)
	if err != nil {
		return snapshot.Commit{}, err
	}
	if len(obj.Content) > storev2.MaxMetadataBytes {
		return snapshot.Commit{}, fmt.Errorf("v1 commit metadata limit")
	}
	var c snapshot.Commit
	if err = decodeV1(obj.Content, &c); err != nil {
		return c, err
	}
	if !v1Hash(c.Tree) || c.Parent != "" && !v1Hash(c.Parent) {
		return c, fmt.Errorf("invalid v1 commit links")
	}
	if c.Hash != "" {
		embedded := c.Hash
		c.Hash = ""
		data, err := json.Marshal(c)
		if err != nil || snapshot.ComputeHash(data) != embedded {
			return c, fmt.Errorf("corrupt embedded v1 commit hash")
		}
		c.Hash = embedded
	}
	return c, nil
}

func mappedCommit(c snapshot.Commit, tree storev2.ID, parents []storev2.ID) storev2.CommitData {
	return storev2.CommitData{Tree: tree, Parents: parents, Author: c.Author, Email: c.Email, Seconds: c.Timestamp.Unix(), Nanoseconds: uint32(c.Timestamp.Nanosecond()), Offset: c.Timestamp.Format("-0700"), Message: c.Message}
}

func (m *v1Migration) commit(ctx context.Context, hash string, depth int) (storev2.ID, error) {
	if id, ok := m.ids[hash]; ok {
		if id.Kind != storev2.Commit {
			return storev2.ID{}, fmt.Errorf("v1 kind collision")
		}
		return id, nil
	}
	if m.active[hash] {
		return storev2.ID{}, fmt.Errorf("v1 commit cycle")
	}
	if depth >= storev2.MaxGraphDepth || len(m.ids)+len(m.active) >= storev2.MaxGraphObjects {
		return storev2.ID{}, fmt.Errorf("v1 graph resource limit")
	}
	m.active[hash] = true
	defer delete(m.active, hash)
	c, err := m.commitData(ctx, hash)
	if err != nil {
		return storev2.ID{}, err
	}
	m.metadata += len(c.Author) + len(c.Email) + len(c.Message) + 256
	if m.metadata > storev2.MaxGraphMetadata {
		return storev2.ID{}, fmt.Errorf("v1 metadata resource limit")
	}
	parents := []storev2.ID{}
	if c.Parent != "" {
		parent, err := m.commit(ctx, c.Parent, depth+1)
		if err != nil {
			return storev2.ID{}, err
		}
		parents = append(parents, parent)
	}
	tree, err := m.tree(ctx, c.Tree)
	if err != nil {
		return storev2.ID{}, err
	}
	id, err := m.destination.store.CreateCommit(ctx, mappedCommit(c, tree, parents))
	if err == nil {
		m.ids[hash] = id
	}
	return id, err
}

func (m *v1Migration) treeEntries(ctx context.Context, hash string) ([]snapshot.TreeEntry, error) {
	obj, err := m.source.object(ctx, hash, snapshot.ObjectTypeTree)
	if err != nil {
		return nil, err
	}
	if len(obj.Content) > storev2.MaxMetadataBytes {
		return nil, fmt.Errorf("v1 tree metadata limit")
	}
	var entries []snapshot.TreeEntry
	if err = decodeV1(obj.Content, &entries); err != nil {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, fmt.Errorf("v1 tree entry limit")
	}
	for _, entry := range entries {
		if entry.Type != snapshot.ObjectTypeBlob || !v1Hash(entry.Hash) {
			return nil, fmt.Errorf("unsupported v1 entry %s", entry.Name)
		}
		if entry.Mode != "-rw-r--r--" && entry.Mode != "-rwxr-xr-x" {
			return nil, fmt.Errorf("unrepresentable v1 mode %q at %s", entry.Mode, entry.Name)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (m *v1Migration) tree(ctx context.Context, hash string) (storev2.ID, error) {
	if id, ok := m.ids[hash]; ok {
		if id.Kind != storev2.Tree {
			return storev2.ID{}, fmt.Errorf("v1 kind collision")
		}
		return id, nil
	}
	entries, err := m.treeEntries(ctx, hash)
	if err != nil {
		return storev2.ID{}, err
	}
	target := make([]storev2.TreeEntry, len(entries))
	for i, entry := range entries {
		target[i] = storev2.TreeEntry{Path: entry.Name, Executable: entry.Mode == "-rwxr-xr-x"}
	}
	if err = validateEntries(target); err != nil {
		return storev2.ID{}, err
	}
	// Probe each historical tree independently: cross-revision file/directory
	// transitions are not collisions within a snapshot.
	probe, err := os.MkdirTemp(filepath.Dir(m.destination.root), "probe-")
	if err != nil {
		return storev2.ID{}, err
	}
	err = preflightFilesystemPaths(probe, target)
	err = errors.Join(err, os.RemoveAll(probe))
	if err != nil {
		return storev2.ID{}, err
	}
	for i, entry := range entries {
		id, err := m.blob(ctx, entry.Hash)
		if err != nil {
			return storev2.ID{}, err
		}
		target[i].Manifest = id
	}
	id, err := m.destination.store.CreateTree(ctx, target)
	if err == nil {
		m.ids[hash] = id
	}
	return id, err
}

func (m *v1Migration) blob(ctx context.Context, hash string) (storev2.ID, error) {
	if id, ok := m.ids[hash]; ok {
		if id.Kind != storev2.Manifest {
			return storev2.ID{}, fmt.Errorf("v1 kind collision")
		}
		return id, nil
	}
	if len(m.ids) >= storev2.MaxGraphObjects {
		return storev2.ID{}, fmt.Errorf("v1 graph resource limit")
	}
	obj, err := m.source.object(ctx, hash, snapshot.ObjectTypeBlob)
	if err != nil {
		return storev2.ID{}, err
	}
	id, err := m.destination.store.IngestFile(ctx, bytes.NewReader(obj.Content), nil)
	if err == nil {
		m.ids[hash] = id
	}
	return id, err
}

// compareBytes checks actual restored bytes, not just metadata/checksum claims.
type compareBytes struct{ remaining []byte }

func (c *compareBytes) Write(p []byte) (int, error) {
	if len(p) > len(c.remaining) || !bytes.Equal(p, c.remaining[:len(p)]) {
		return 0, fmt.Errorf("restored bytes differ")
	}
	c.remaining = c.remaining[len(p):]
	return len(p), nil
}

func (m *v1Migration) verify(ctx context.Context, hash string, id storev2.ID) error {
	switch id.Kind {
	case storev2.Manifest:
		obj, err := m.source.object(ctx, hash, snapshot.ObjectTypeBlob)
		if err != nil {
			return err
		}
		comparison := &compareBytes{remaining: obj.Content}
		digest := sha256.New()
		if err = m.destination.store.CopyFile(ctx, id, io.MultiWriter(comparison, digest)); err != nil {
			return err
		}
		expected := sha256.Sum256(obj.Content)
		length, sum, err := m.destination.store.FileInfo(ctx, id)
		if err != nil {
			return err
		}
		if len(comparison.remaining) != 0 || length != uint64(len(obj.Content)) || sum != expected || !bytes.Equal(digest.Sum(nil), expected[:]) {
			return fmt.Errorf("file length/checksum/bytes mismatch")
		}
	case storev2.Tree:
		source, err := m.treeEntries(ctx, hash)
		if err != nil {
			return err
		}
		target, err := m.destination.store.ReadTree(ctx, id)
		if err != nil {
			return err
		}
		if len(source) != len(target) {
			return fmt.Errorf("tree entry count mismatch")
		}
		for i, entry := range source {
			if target[i].Path != entry.Name || target[i].Executable != (entry.Mode == "-rwxr-xr-x") || target[i].Manifest != m.ids[entry.Hash] {
				return fmt.Errorf("tree mapping mismatch: %s", entry.Name)
			}
		}
	case storev2.Commit:
		source, err := m.commitData(ctx, hash)
		if err != nil {
			return err
		}
		parents := []storev2.ID{}
		if source.Parent != "" {
			parents = append(parents, m.ids[source.Parent])
		}
		expected := mappedCommit(source, m.ids[source.Tree], parents)
		target, err := m.destination.store.ReadCommit(ctx, id)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(expected, target) {
			return fmt.Errorf("commit metadata/graph mapping mismatch")
		}
	default:
		return fmt.Errorf("unsupported mapping kind")
	}
	return nil
}
