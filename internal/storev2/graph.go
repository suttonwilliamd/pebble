package storev2

import "context"

// Local verification policy, not new wire limits. Fail closed rather than walk
// an unbounded graph or retain unbounded metadata from an untrusted transfer.
const (
	MaxGraphObjects   = 100000
	MaxGraphEdges     = 1000000
	MaxGraphMetadata  = 64 << 20
	MaxGraphDepth     = 4096
	MaxGraphReadBytes = uint64(1 << 40)
)

type graph struct {
	store      *Store
	ctx        context.Context
	buffer     []byte
	nodes      map[ID]*node
	state      map[ID]byte
	metadata   uint64
	edges      int
	readBudget uint64
}

func (s *Store) graph(ctx context.Context, buffer []byte) *graph {
	return &graph{store: s, ctx: ctx, buffer: buffer, nodes: make(map[ID]*node), state: make(map[ID]byte), readBudget: MaxGraphReadBytes}
}

// Verify validates complete dependency closure, including reconstructed file
// lengths/checksums and every parent/tag target. It never creates or changes refs.
func (s *Store) Verify(ctx context.Context, root ID) error {
	if err := s.guard(); err != nil {
		return err
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	return s.graph(ctx, buffer[:]).verify(root)
}

func (g *graph) verify(root ID) error {
	type frame struct {
		id   ID
		next int
	}
	stack := []frame{{id: root}}
	for len(stack) != 0 {
		if err := g.ctx.Err(); err != nil {
			return err
		}
		top := &stack[len(stack)-1]
		if g.state[top.id] == 2 {
			stack = stack[:len(stack)-1]
			continue
		}
		n := g.nodes[top.id]
		if n == nil {
			if len(g.nodes) >= MaxGraphObjects || len(stack) > MaxGraphDepth {
				return ErrGraphLimit
			}
			var err error
			n, err = g.store.load(g.ctx, top.id, nil, nil, g.buffer, &g.readBudget)
			if err != nil {
				return err
			}
			weight := uint64(128 + len(n.edges)*48)
			if n.kind != Chunk {
				weight += n.size
			}
			if weight > MaxGraphMetadata-g.metadata || len(n.edges) > MaxGraphEdges-g.edges {
				return ErrGraphLimit
			}
			g.metadata += weight
			g.edges += len(n.edges)
			g.nodes[top.id], g.state[top.id] = n, 1
		}
		if top.next < len(n.edges) {
			child := n.edges[top.next].id
			top.next++
			if g.state[child] == 1 {
				return corrupt(top.id, invalid("cycle"))
			}
			if g.state[child] == 0 {
				stack = append(stack, frame{id: child})
			}
			continue
		}
		if err := g.semantic(top.id, n); err != nil {
			return err
		}
		g.state[top.id] = 2
		stack = stack[:len(stack)-1]
	}
	return nil
}

func (g *graph) semantic(id ID, n *node) error {
	switch n.kind {
	case ManifestPage:
		for _, e := range n.edges {
			if g.nodes[e.id].size != e.length {
				return corrupt(id, invalid("length"))
			}
		}
	case Manifest:
		return g.store.copyManifest(g.ctx, id, n, nil, g.buffer, &g.readBudget, g.nodes)
	}
	return nil
}

func (g *graph) ancestor(old, next ID) bool {
	pending := []ID{next}
	seen := make(map[ID]bool)
	for len(pending) != 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if id == old {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		n := g.nodes[id]
		if n == nil || n.kind != Commit {
			continue
		}
		for _, parent := range n.edges[1:] {
			pending = append(pending, parent.id)
		}
	}
	return false
}

func (g *graph) makeDurable() error {
	for id := range g.nodes {
		if err := g.ctx.Err(); err != nil {
			return err
		}
		if g.store.knownDurable(id) {
			continue
		}
		if err := g.store.reaffirm(g.ctx, id, g.buffer, &g.readBudget); err != nil {
			return err
		}
	}
	return nil
}
