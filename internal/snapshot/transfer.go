package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

const TransferVersion = 1
const MaxTransferObjects = 50000

var ErrRefConflict = errors.New("branch changed or update is not fast-forward")
var ErrInvalidBundle = errors.New("invalid transfer bundle")

// Bundle transfers a complete, typed, content-addressed history for one branch.
// Version 1 is deliberately bounded and non-incremental, for small test repositories.
type Bundle struct {
	Version int       `json:"version"`
	Ref     string    `json:"ref"`
	Hash    string    `json:"hash"`
	Objects []*Object `json:"objects"`
}

func ValidateRef(ref string) error { return validateRef(ref) }

func checkRefCollision(ref string, refs []Ref) error {
	key := strings.ToLower(ref)
	for _, existing := range refs {
		old := strings.ToLower(existing.Name)
		if old == key && existing.Name != ref || strings.HasPrefix(old, key+"/") || strings.HasPrefix(key, old+"/") {
			return fmt.Errorf("branch name collides with %s", existing.Name)
		}
	}
	return nil
}

// walkGraph validates both object integrity and dependency types without recursive traversal.
func walkGraph(root string, get func(string) (*Object, error)) (map[string]*Object, error) {
	type want struct {
		hash string
		kind ObjectType
	}
	queue := []want{{root, ObjectTypeCommit}}
	seen := map[string]*Object{}
	for len(queue) > 0 {
		w := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if obj, ok := seen[w.hash]; ok {
			if obj.Type != w.kind {
				return nil, fmt.Errorf("conflicting object types for %s", w.hash)
			}
			continue
		}
		if len(seen) >= MaxTransferObjects {
			return nil, fmt.Errorf("history exceeds %d objects", MaxTransferObjects)
		}
		obj, err := get(w.hash)
		if err != nil {
			return nil, err
		}
		if err = validateObject(obj); err != nil {
			return nil, err
		}
		if obj.Hash != w.hash || obj.Type != w.kind {
			return nil, fmt.Errorf("unexpected object address or type: %s", w.hash)
		}
		seen[w.hash] = obj
		switch obj.Type {
		case ObjectTypeCommit:
			var c Commit
			if err = json.Unmarshal(obj.Content, &c); err != nil {
				return nil, err
			}
			queue = append(queue, want{c.Tree, ObjectTypeTree})
			if c.Parent != "" {
				queue = append(queue, want{c.Parent, ObjectTypeCommit})
			}
		case ObjectTypeTree:
			var entries []TreeEntry
			if err = json.Unmarshal(obj.Content, &entries); err != nil {
				return nil, err
			}
			if len(entries)+len(queue) > MaxTransferObjects {
				return nil, fmt.Errorf("too many graph dependencies")
			}
			if err = validateEntries(entries); err != nil {
				return nil, err
			}
			for _, e := range entries {
				queue = append(queue, want{e.Hash, ObjectTypeBlob})
			}
		}
	}
	return seen, nil
}

func (r *Repository) Export(ref string) (*Bundle, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	hash, err := r.GetRef(ref)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		return nil, fmt.Errorf("branch has no commits: %s", ref)
	}
	graph, err := walkGraph(hash, r.GetObject)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(graph))
	for h := range graph {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	b := &Bundle{Version: TransferVersion, Ref: ref, Hash: hash, Objects: make([]*Object, 0, len(graph))}
	for _, h := range hashes {
		b.Objects = append(b.Objects, graph[h])
	}
	return b, nil
}

func (b *Bundle) graph() (map[string]*Object, error) {
	if b == nil || b.Version != TransferVersion {
		return nil, fmt.Errorf("unsupported transfer version")
	}
	if err := validateRef(b.Ref); err != nil {
		return nil, err
	}
	if !validHash(b.Hash) {
		return nil, fmt.Errorf("invalid tip hash")
	}
	if len(b.Objects) > MaxTransferObjects {
		return nil, fmt.Errorf("too many objects")
	}
	objects := map[string]*Object{}
	for _, obj := range b.Objects {
		if err := validateObject(obj); err != nil {
			return nil, err
		}
		if _, ok := objects[obj.Hash]; ok {
			return nil, fmt.Errorf("duplicate object %s", obj.Hash)
		}
		objects[obj.Hash] = obj
	}
	graph, err := walkGraph(b.Hash, func(hash string) (*Object, error) {
		obj, ok := objects[hash]
		if !ok {
			return nil, fmt.Errorf("missing dependency %s", hash)
		}
		return obj, nil
	})
	if err != nil {
		return nil, err
	}
	if len(graph) != len(objects) {
		return nil, fmt.Errorf("bundle contains unreachable objects")
	}
	return graph, nil
}

// ApplyBundle verifies a complete graph before storing anything, then publishes a
// single fast-forward ref update with an expected-old-hash compare under the writer lock.
// It never changes HEAD or working files.
func (r *Repository) ApplyBundle(b *Bundle, expected string) error {
	return r.applyBundle(b, expected, false)
}

// ImportBundle additionally refuses to move the checked-out branch, checking
// HEAD under the same writer lock as the ref update (including concurrent checkout).
func (r *Repository) ImportBundle(b *Bundle, expected string) error {
	return r.applyBundle(b, expected, true)
}

func (r *Repository) applyBundle(b *Bundle, expected string, protectWorktree bool) error {
	graph, err := b.graph()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	if expected != "" && !validHash(expected) {
		return fmt.Errorf("%w: invalid expected hash", ErrInvalidBundle)
	}
	unlock, err := r.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if protectWorktree {
		head, err := r.GetHead()
		if err != nil {
			return err
		}
		if head == b.Ref {
			return fmt.Errorf("cannot import into checked-out branch %s; checkout a work branch first", head)
		}
	}
	current, err := r.GetRef(b.Ref)
	if os.IsNotExist(err) {
		current = ""
	} else if err != nil {
		return err
	}
	if current != expected {
		return ErrRefConflict
	}
	if current != "" {
		tip := b.Hash
		for tip != current {
			if tip == "" {
				return ErrRefConflict
			}
			obj := graph[tip]
			if obj == nil || obj.Type != ObjectTypeCommit {
				return ErrRefConflict
			}
			var c Commit
			if err = json.Unmarshal(obj.Content, &c); err != nil {
				return err
			}
			tip = c.Parent
		}
	}
	// Refuse file/directory branch collisions and nonportable case variants.
	refs, err := r.GetRefs()
	if err != nil {
		return err
	}
	if err = checkRefCollision(b.Ref, refs); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	for _, obj := range graph {
		if err = r.StoreObject(obj); err != nil {
			return err
		}
	}
	return r.SetRef(b.Ref, b.Hash)
}
