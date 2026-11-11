// Package formatcontract is an executable PB-01 specification only.
// No production implementation may import this test-only decoder.
package formatcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

const maxLength = uint64(1<<63 - 1)

var kinds = map[byte]string{1: "chunk", 2: "manifest-page", 3: "manifest", 4: "tree", 5: "commit", 6: "tag"}

type vector struct {
	Name         string   `json:"name"`
	Note         string   `json:"note"`
	Family       string   `json:"family,omitempty"`
	WireHex      string   `json:"wire_hex"`
	ID           string   `json:"id,omitempty"`
	Error        string   `json:"error"`
	Dependencies []string `json:"dependencies,omitempty"`
	Offers       []string `json:"offers,omitempty"`
}

type link struct {
	kind   byte
	digest [32]byte
}

func (l link) id() string                 { return fmt.Sprintf("pv2:%s:sha256:%x", kinds[l.kind], l.digest) }
func address(kind byte, wire []byte) link { return link{kind, sha256.Sum256(wire)} }

// Rejections are stable corpus categories, not a production API/error taxonomy.
func reject(category string) { panic(category) }
func check(ok bool, category string) {
	if !ok {
		reject(category)
	}
}
func outcome(fn func()) (result string) {
	defer func() {
		if r := recover(); r != nil {
			if s, ok := r.(string); ok {
				result = s
			} else {
				panic(r)
			}
		}
	}()
	fn()
	return ""
}

type cursor struct{ b []byte }

func (c *cursor) take(n uint64) []byte {
	check(n <= uint64(len(c.b)), "truncated")
	b := c.b[:int(n)]
	c.b = c.b[int(n):]
	return b
}
func (c *cursor) u8() byte    { return c.take(1)[0] }
func (c *cursor) u16() uint16 { return binary.BigEndian.Uint16(c.take(2)) }
func (c *cursor) u32() uint32 { return binary.BigEndian.Uint32(c.take(4)) }
func (c *cursor) u64() uint64 { return binary.BigEndian.Uint64(c.take(8)) }
func (c *cursor) count(max uint32) int {
	n := c.u32()
	check(n <= max, "limit")
	return int(n)
}
func (c *cursor) text(message bool) string {
	n := c.u32()
	check(n <= 65535, "limit")
	b := c.take(uint64(n))
	check(utf8.Valid(b), "text")
	for _, r := range string(b) {
		check(!(r < 32 || r >= 127 && r <= 159) || message && (r == '\n' || r == '\t'), "text")
	}
	return string(b)
}
func (c *cursor) ref(want ...byte) link {
	l := link{kind: c.u8()}
	check(kinds[l.kind] != "", "id-type")
	allowed := false
	for _, k := range want {
		allowed = allowed || l.kind == k
	}
	check(allowed, "id-type")
	copy(l.digest[:], c.take(32))
	return l
}
func (c *cursor) versions() []uint16 {
	n := c.count(16)
	check(n > 0, "version")
	v := make([]uint16, n)
	for i := range v {
		v[i] = c.u16()
		check(v[i] > 0 && (i == 0 || v[i-1] < v[i]), "version")
	}
	return v
}
func (c *cursor) features() []string {
	n := c.count(32)
	v := make([]string, n)
	for i := range v {
		v[i] = c.text(false)
		check(v[i] != "" && (i == 0 || v[i-1] < v[i]), "feature")
		for _, r := range v[i] {
			check(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-', "feature")
		}
	}
	return v
}

type record struct {
	kind               byte
	payload            []byte
	links              []link
	lengths            []uint64
	total              uint64
	checksum           [32]byte
	names              []string
	name               string
	formats, protocols []uint16
	features           []string
	expected           *link
	time               timestamp
}

type timestamp struct {
	seconds     int64
	nanoseconds uint32
	offset      string
}

func validPath(p string) bool {
	if len(p) == 0 || len(p) > 4096 || strings.ContainsAny(p, "\\:") {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." || strings.EqualFold(s, ".git") || strings.EqualFold(s, ".pebble") {
			return false
		}
	}
	return true
}
func validRef(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] != "heads" && parts[0] != "tags" || !validPath(s) {
		return false
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") || strings.HasSuffix(p, ".lock") {
			return false
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return false
			}
		}
	}
	return true
}
func identity(c *cursor) timestamp {
	check(c.text(false) != "", "identity")
	check(c.text(false) != "", "identity")
	stamp := timestamp{seconds: int64(c.u64()), nanoseconds: c.u32()}
	check(stamp.nanoseconds < 1000000000, "timestamp")
	offset := c.take(5)
	check(offset[0] == '+' || offset[0] == '-', "timestamp")
	for _, digit := range offset[1:] {
		check(digit >= '0' && digit <= '9', "timestamp")
	}
	check((offset[1]-'0')*10+offset[2]-'0' <= 23 && (offset[3]-'0')*10+offset[4]-'0' <= 59, "timestamp")
	stamp.offset = string(offset)
	c.text(true)
	return stamp
}

func decode(wire []byte) *record {
	c := &cursor{b: wire}
	check(bytes.Equal(c.take(4), []byte{'P', 'B', '2', 0}), "version")
	r := &record{kind: c.u8()}
	check(kinds[r.kind] != "" || r.kind >= 0x80 && r.kind <= 0x82, "kind")
	n := c.u64()
	limit := uint64(1 << 20)
	if r.kind == 1 {
		limit = 8 << 20
	}
	check(n <= limit, "limit")
	r.payload = c.take(n)
	check(len(c.b) == 0, "trailing")
	c = &cursor{b: r.payload}
	switch r.kind {
	case 1:
		c.take(uint64(len(c.b)))
	case 2, 3:
		if r.kind == 3 {
			r.total = c.u64()
			check(r.total <= maxLength, "length")
			copy(r.checksum[:], c.take(32))
		}
		max, want := uint32(1024), byte(1)
		if r.kind == 3 {
			max, want = 16384, 2
		}
		n := c.count(max)
		check(r.kind != 2 || n > 0, "length")
		var total uint64
		for i := 0; i < n; i++ {
			r.links = append(r.links, c.ref(want))
			size := c.u64()
			check(size > 0 && size <= maxLength-total, "length")
			check(r.kind != 2 || size <= 8<<20, "length")
			total += size
			r.lengths = append(r.lengths, size)
		}
		if r.kind == 3 {
			check(total == r.total, "length")
		} else {
			r.total = total
		}
	case 4:
		n := c.count(10000)
		seen := map[string]bool{}
		for i := 0; i < n; i++ {
			p := c.text(false)
			check(validPath(p), "path")
			check(i == 0 || r.names[i-1] < p, "order")
			parts := strings.Split(p, "/")
			for j := 1; j < len(parts); j++ {
				check(!seen[strings.Join(parts[:j], "/")], "path")
			}
			seen[p] = true
			r.names = append(r.names, p)
			check(c.u8() <= 1, "mode")
			r.links = append(r.links, c.ref(3))
		}
	case 5:
		r.links = append(r.links, c.ref(4))
		n := c.count(64)
		seen := map[link]bool{}
		for i := 0; i < n; i++ {
			p := c.ref(5)
			check(!seen[p], "parents")
			seen[p] = true
			r.links = append(r.links, p)
		}
		r.time = identity(c)
	case 6:
		r.name = c.text(false)
		check(validRef("tags/"+r.name), "ref")
		r.links = append(r.links, c.ref(1, 2, 3, 4, 5, 6))
		r.time = identity(c)
	case 0x80:
		r.formats, r.protocols, r.features = c.versions(), c.versions(), c.features()
	case 0x81:
		r.formats, r.protocols = []uint16{c.u16()}, []uint16{c.u16()}
		r.features = c.features()
	case 0x82:
		r.name = c.text(false)
		check(validRef(r.name), "ref")
		present := c.u8()
		check(present <= 1, "presence")
		want := []byte{5}
		if strings.HasPrefix(r.name, "tags/") {
			want = []byte{1, 2, 3, 4, 5, 6}
			check(present == 0, "tag-update")
		}
		if present == 1 {
			old := c.ref(want...)
			r.expected = &old
		}
		r.links = append(r.links, c.ref(want...))
	}
	check(len(c.b) == 0, "trailing")
	return r
}

func verifyGraph(root *record, objects map[link]*record) {
	active, done := map[*record]bool{}, map[*record]bool{}
	var visit func(*record)
	visit = func(r *record) {
		check(!active[r], "cycle")
		if done[r] {
			return
		}
		active[r] = true
		for _, l := range r.links {
			child := objects[l]
			check(child != nil, "missing")
			visit(child)
		}
		if r.kind == 2 {
			for i, l := range r.links {
				check(uint64(len(objects[l].payload)) == r.lengths[i], "length")
			}
		}
		if r.kind == 3 {
			h := sha256.New()
			for i, l := range r.links {
				page := objects[l]
				check(page.total == r.lengths[i], "length")
				check(i == len(r.links)-1 || len(page.links) == 1024, "partition")
				writePage(h, page, objects)
			}
			check(bytes.Equal(h.Sum(nil), r.checksum[:]), "checksum")
		}
		if r.kind == 0x82 && strings.HasPrefix(r.name, "tags/") {
			target := objects[r.links[0]]
			check(target.kind != 6 || target.name == strings.TrimPrefix(r.name, "tags/"), "tag-name")
		}
		delete(active, r)
		done[r] = true
	}
	visit(root)
}
func writePage(h hash.Hash, page *record, objects map[link]*record) {
	for _, l := range page.links {
		h.Write(objects[l].payload)
	}
}
func contains(v []uint16, want uint16) bool {
	for _, n := range v {
		if n == want {
			return true
		}
	}
	return false
}
func highest(a, b []uint16) uint16 {
	var result uint16
	for _, n := range a {
		if contains(b, n) && n > result {
			result = n
		}
	}
	return result
}
func negotiate(selection, a, b *record) {
	check(a.kind == 0x80 && b.kind == 0x80, "version")
	check(selection.formats[0] == 2 && selection.protocols[0] == 2 && highest(a.formats, b.formats) == 2 && highest(a.protocols, b.protocols) == 2, "version")
	check(strings.Join(selection.features, ",") == "paged-manifests,typed-ids", "feature")
	for _, f := range selection.features {
		check(sort.SearchStrings(a.features, f) < len(a.features) && a.features[sort.SearchStrings(a.features, f)] == f, "feature")
		check(sort.SearchStrings(b.features, f) < len(b.features) && b.features[sort.SearchStrings(b.features, f)] == f, "feature")
	}
}

func TestV2GoldenContract(t *testing.T) {
	data, err := os.ReadFile("testdata/v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Contract string   `json:"contract"`
		Vectors  []vector `json:"vectors"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Contract != "pebble-v2-pb01" {
		t.Fatal("wrong corpus")
	}
	byName := map[string]vector{}
	for _, v := range corpus.Vectors {
		if _, exists := byName[v.Name]; exists {
			t.Fatalf("duplicate fixture %s", v.Name)
		}
		byName[v.Name] = v
	}
	wire := func(v vector) []byte {
		b, err := hex.DecodeString(v.WireHex)
		if err != nil || hex.EncodeToString(b) != v.WireHex {
			t.Fatalf("%s: noncanonical fixture hex: %v", v.Name, err)
		}
		return b
	}
	for _, v := range corpus.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			b := wire(v)
			got := outcome(func() {
				if v.Family == "marker" {
					check(bytes.Equal(b, []byte("pebble-format 2\n")), "version")
					return
				}
				r := decode(b)
				if kinds[r.kind] != "" {
					check(address(r.kind, b).id() == v.ID, "address")
				} else {
					check(v.ID == "", "address")
				}
				objects := map[link]*record{}
				added := map[string]bool{}
				var add func(string)
				add = func(name string) {
					if added[name] {
						return
					}
					added[name] = true
					dep, ok := byName[name]
					check(ok && dep.Error == "", "fixture-dependency")
					db := wire(dep)
					dr := decode(db)
					check(address(dr.kind, db).id() == dep.ID, "fixture-address")
					objects[address(dr.kind, db)] = dr
					for _, child := range dep.Dependencies {
						add(child)
					}
				}
				for _, dep := range v.Dependencies {
					add(dep)
				}
				verifyGraph(r, objects)
				if r.kind == 0x81 {
					check(len(v.Offers) == 2, "fixture-offer")
					negotiate(r, decode(wire(byName[v.Offers[0]])), decode(wire(byName[v.Offers[1]])))
				}
			})
			if got != v.Error {
				t.Fatalf("want rejection %q, got %q; %s", v.Error, got, v.Note)
			}
		})
	}
	t.Run("identity-and-revision-invariants", func(t *testing.T) {
		get := func(name string) *record {
			v, ok := byName[name]
			if !ok || v.Error != "" {
				t.Fatalf("missing valid fixture %q", name)
			}
			return decode(wire(v))
		}
		chunk, tree := get("chunk-tree-payload"), get("tree-empty")
		if !bytes.Equal(chunk.payload, tree.payload) || byName["chunk-tree-payload"].ID == byName["tree-empty"].ID {
			t.Fatal("equal payloads across kinds must have distinct typed identities")
		}
		old, next := get("tree-old"), get("tree-new")
		if old.names[0] != "asset.bin" || next.names[0] != old.names[0] ||
			old.links[0] != address(3, wire(byName["manifest-old"])) ||
			next.links[0] != address(3, wire(byName["manifest-new"])) || old.links[0] == next.links[0] {
			t.Fatal("same-path revisions lost their independent immutable manifests")
		}
		if get("manifest-old").total != 1 || get("manifest-old").checksum != sha256.Sum256([]byte("A")) ||
			get("manifest-new").total != 5 || get("manifest-new").checksum != sha256.Sum256([]byte{0, 255, 10, 13, 128}) {
			t.Fatal("revision length/checksum changed")
		}
		merge, reverse := get("commit-merge"), get("commit-merge-reversed")
		first, second := address(5, wire(byName["commit-new"])), address(5, wire(byName["commit-side"]))
		if merge.links[1] != first || merge.links[2] != second || reverse.links[1] != second || reverse.links[2] != first ||
			byName["commit-merge"].ID == byName["commit-merge-reversed"].ID {
			t.Fatal("parent order is identity-significant and must not be sorted")
		}
		if get("commit-pre-epoch").time != (timestamp{-1, 500000000, "-0530"}) ||
			get("commit-min-time").time.seconds != -1<<63 ||
			get("commit-max-nanos").time != (timestamp{1<<63 - 1, 999999999, "+2359"}) {
			t.Fatal("signed timestamp, fractional precision or offset was lost")
		}
		if get("commit-plus-zero").time.offset != "+0000" || get("commit-minus-zero").time.offset != "-0000" ||
			byName["commit-plus-zero"].ID == byName["commit-minus-zero"].ID {
			t.Fatal("original zero-offset spelling must remain identity-significant")
		}
		if get("tag-nested").links[0] != address(6, wire(byName["tag-annotated"])) ||
			get("tag-tree").links[0] != address(4, wire(byName["tree-empty"])) ||
			get("tag-file-manifest").links[0] != address(3, wire(byName["manifest-new"])) {
			t.Fatal("typed tag targets must not be implicitly peeled or flattened")
		}
	})
}
