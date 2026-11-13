package storev2

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

const maxLength = uint64(1<<63 - 1)

type linkTargetWriter struct{}

func (linkTargetWriter) Write(p []byte) (int, error) {
	if bytes.IndexByte(p, 0) >= 0 {
		return 0, invalid("link-target")
	}
	return len(p), nil
}

type edge struct {
	id     ID
	mode   byte // Tree-only; occupies existing alignment padding before length.
	length uint64
}
type node struct {
	kind     Kind
	size     uint64
	total    uint64
	checksum [32]byte
	edges    []edge
	tagName  string
	symlinks bool
}

// decoder records the first failure. Every read is bounded before indexing;
// malformed input never uses panic/recover as a validation mechanism.
type decoder struct {
	b   []byte
	err error
}

func (d *decoder) require(ok bool, rule string) {
	if !ok && d.err == nil {
		d.err = invalid(rule)
	}
}
func (d *decoder) take(n uint64) []byte {
	if d.err != nil {
		return nil
	}
	if n > uint64(len(d.b)) {
		d.err = invalid("truncated")
		return nil
	}
	b := d.b[:int(n)]
	d.b = d.b[int(n):]
	return b
}
func (d *decoder) u8() byte {
	b := d.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (d *decoder) u32() uint32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}
func (d *decoder) u64() uint64 {
	b := d.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
func (d *decoder) count(max uint32) int {
	n := d.u32()
	d.require(n <= max, "limit")
	if d.err != nil {
		return 0
	}
	return int(n)
}
func (d *decoder) text(message bool) []byte {
	n := d.u32()
	d.require(n <= 65535, "limit")
	b := d.take(uint64(n))
	d.require(utf8.Valid(b), "text")
	for _, r := range string(b) {
		d.require(!(r < 32 || r >= 127 && r <= 159) || message && (r == '\n' || r == '\t'), "text")
	}
	return b
}
func (d *decoder) link(want Kind) ID {
	id := ID{Kind: Kind(d.u8())}
	d.require(id.Kind.valid() && (want == 0 || id.Kind == want), "id-type")
	copy(id.Digest[:], d.take(32))
	return id
}
func (d *decoder) identity() {
	d.require(len(d.text(false)) > 0, "identity")
	d.require(len(d.text(false)) > 0, "identity")
	d.u64() // All two's-complement signed-second bit patterns are valid.
	d.require(d.u32() < 1000000000, "timestamp")
	offset := d.take(5)
	if len(offset) == 5 {
		d.require(offset[0] == '+' || offset[0] == '-', "timestamp")
		for _, digit := range offset[1:] {
			d.require(digit >= '0' && digit <= '9', "timestamp")
		}
		d.require((int(offset[1])-'0')*10+int(offset[2])-'0' <= 23 && (int(offset[3])-'0')*10+int(offset[4])-'0' <= 59, "timestamp")
	}
	d.text(true)
}

func validPath(p string) bool {
	if len(p) == 0 || len(p) > 4096 || strings.ContainsAny(p, "\\:") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") || strings.EqualFold(part, ".pebble") {
			return false
		}
	}
	return true
}
func validRef(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) < 2 || parts[0] != "heads" && parts[0] != "tags" || !validPath(name) {
		return false
	}
	for _, part := range parts[1:] {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

func parsePayload(kind Kind, payload []byte) (*node, error) {
	n := &node{kind: kind, size: uint64(len(payload))}
	if kind == Chunk {
		return n, nil
	}
	d := decoder{b: payload}
	switch kind {
	case ManifestPage, Manifest:
		max, childKind := uint32(manifestPageEntries), Chunk
		if kind == Manifest {
			n.total = d.u64()
			d.require(n.total <= maxLength, "length")
			copy(n.checksum[:], d.take(32))
			max, childKind = maxManifestPages, ManifestPage
		}
		count := d.count(max)
		d.require(kind != ManifestPage || count > 0, "length")
		d.require(count <= len(d.b)/41, "truncated")
		if d.err != nil {
			return nil, d.err
		}
		n.edges = make([]edge, 0, count)
		var total uint64
		for i := 0; i < count && d.err == nil; i++ {
			id, length := d.link(childKind), d.u64()
			d.require(length > 0 && length <= maxLength-total, "length")
			d.require(kind != ManifestPage || length <= MaxChunkBytes, "length")
			if d.err != nil {
				break
			}
			total += length
			n.edges = append(n.edges, edge{id: id, length: length})
		}
		if kind == Manifest {
			d.require(total == n.total, "length")
			if count == 0 {
				d.require(n.checksum == sha256.Sum256(nil), "checksum")
			}
		} else {
			n.total = total
		}
	case Tree:
		count := d.count(10000)
		d.require(count <= len(d.b)/39, "truncated")
		if d.err != nil {
			return nil, d.err
		}
		n.edges = make([]edge, 0, count)
		seen := make(map[string]struct{}, count)
		var previous []byte
		for i := 0; i < count && d.err == nil; i++ {
			pathBytes := d.text(false)
			path := string(pathBytes)
			d.require(validPath(path), "path")
			d.require(i == 0 || bytes.Compare(previous, pathBytes) < 0, "order")
			for end := strings.LastIndexByte(path, '/'); end >= 0; end = strings.LastIndexByte(path[:end], '/') {
				_, exists := seen[path[:end]]
				d.require(!exists, "path")
			}
			seen[path] = struct{}{}
			previous = pathBytes
			mode := d.u8()
			d.require(mode <= 3, "mode")
			n.symlinks = n.symlinks || mode >= 2
			n.edges = append(n.edges, edge{id: d.link(Manifest), mode: mode})
		}
	case Commit:
		tree := d.link(Tree)
		count := d.count(64)
		d.require(count <= len(d.b)/33, "truncated")
		if d.err != nil {
			return nil, d.err
		}
		n.edges = make([]edge, 1, count+1)
		n.edges[0] = edge{id: tree}
		seen := make(map[ID]struct{}, count)
		for i := 0; i < count && d.err == nil; i++ {
			id := d.link(Commit)
			_, exists := seen[id]
			d.require(!exists, "parents")
			seen[id] = struct{}{}
			n.edges = append(n.edges, edge{id: id})
		}
		d.identity()
	case Tag:
		n.tagName = string(d.text(false))
		d.require(validRef("tags/"+n.tagName), "ref")
		n.edges = append(n.edges, edge{id: d.link(0)})
		d.identity()
	default:
		return nil, invalid("kind")
	}
	d.require(len(d.b) == 0, "trailing")
	if d.err != nil {
		return nil, d.err
	}
	return n, nil
}
