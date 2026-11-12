package storev2

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Physical limits are local policy, not logical format-2 object limits.
const (
	MaxPackBytes     = 1 << 30
	MaxPackObjects   = MaxGraphObjects
	MaxPacks         = 1024
	packMagic        = "PBPACK\x00\x01"
	indexMagic       = "PBINDX\x00\x01"
	catalogMagic     = "PBCATL\x00\x01"
	recordHeaderSize = 50 // typed ID, codec, raw length, stored length
)

type packEntry struct {
	id          ID
	codec       byte
	raw, stored uint64
	offset      int64
}
type packInfo struct {
	name    string
	entries []packEntry
}

func idLess(a, b ID) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return bytes.Compare(a.Digest[:], b.Digest[:]) < 0
}
func packName(name string) bool {
	if len(name) != 64 {
		return false
	}
	b, err := hex.DecodeString(name)
	return err == nil && hex.EncodeToString(b) == name
}
func (s *Store) packPath(name, ext string) string { return filepath.Join(s.meta, "pack-"+name+ext) }

// packNames treats complete immutable packs as the recovery source of truth.
// The catalog may lag a published pack after interruption, but may never claim
// a missing pack. Ordinary staging files are invisible to readers.
func (s *Store) packNames(ctx context.Context, checkCatalog bool) ([]string, error) {
	dir := s.meta
	entries, err := directoryEntries(dir, 2*MaxPacks+MaxPackObjects+2)
	if err != nil {
		return nil, err
	}
	if len(entries) >= 2*MaxPacks+MaxPackObjects+2 {
		return nil, ErrGraphLimit
	}
	names := make([]string, 0)
	indexes := make([]string, 0)
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := e.Name()
		if !strings.HasPrefix(n, "pack-") {
			continue
		}
		n = strings.TrimPrefix(n, "pack-")
		if !e.Type().IsRegular() {
			return nil, invalid("pack directory entry")
		}
		if n == "catalog" {
			continue
		}
		ext := filepath.Ext(n)
		stem := strings.TrimSuffix(n, ext)
		if !packName(stem) || (ext != ".pack" && ext != ".idx") {
			return nil, invalid("pack filename")
		}
		if ext == ".pack" {
			names = append(names, stem)
		} else {
			indexes = append(indexes, stem)
		}
	}
	if len(names) > MaxPacks {
		return nil, ErrGraphLimit
	}
	sort.Strings(names)
	for _, n := range indexes {
		i := sort.SearchStrings(names, n)
		if i == len(names) || names[i] != n {
			return nil, invalid("orphan pack index")
		}
	}
	if checkCatalog {
		b, err := readBoundedFile(s.packPath("catalog", ""), 12+32*MaxPacks+32)
		if err == nil {
			if len(b) < 44 || string(b[:8]) != catalogMagic || !validChecksum(b) {
				return nil, invalid("pack catalog checksum/version")
			}
			count := binary.BigEndian.Uint32(b[8:12])
			if count > MaxPacks || len(b) != 12+32*int(count)+32 {
				return nil, invalid("pack catalog length")
			}
			previous := ""
			for i := 0; i < int(count); i++ {
				n := hex.EncodeToString(b[12+32*i : 12+32*(i+1)])
				at := sort.SearchStrings(names, n)
				if n <= previous || at == len(names) || names[at] != n {
					return nil, invalid("pack catalog membership")
				}
				previous = n
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return names, nil
}
func readBoundedFile(path string, limit int64) (_ []byte, err error) {
	f, err := regular(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, invalid("physical metadata limit")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
func validChecksum(b []byte) bool {
	if len(b) < 32 {
		return false
	}
	sum := sha256.Sum256(b[:len(b)-32])
	return bytes.Equal(sum[:], b[len(b)-32:])
}
func checkedBytes(b []byte) []byte { sum := sha256.Sum256(b); return append(b, sum[:]...) }

// scanPack verifies the physical checksum, framing and canonical ordering before
// trusting offsets. Repair additionally validates every logical object. No bytes
// from a failed scan are eligible for index/catalog publication.
func (s *Store) scanPack(ctx context.Context, name string, validate bool, buffer []byte) (_ packInfo, err error) {
	p := packInfo{name: name}
	f, err := regular(s.packPath(name, ".pack"))
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return p, err
	}
	if info.Size() < 44 || info.Size() > MaxPackBytes {
		return p, invalid("pack size")
	}
	h := sha256.New()
	r := &contextReader{ctx: ctx, reader: io.TeeReader(io.NewSectionReader(f, 0, info.Size()-32), h)}
	var head [12]byte
	if _, err = r.readFull(head[:]); err != nil {
		return p, err
	}
	count := binary.BigEndian.Uint32(head[8:])
	if string(head[:8]) != packMagic || count == 0 || count > MaxPackObjects {
		return p, invalid("pack version/count")
	}
	p.entries = make([]packEntry, 0, int(count))
	offset := int64(12)
	for i := uint32(0); i < count; i++ {
		var header [recordHeaderSize]byte
		if _, err = r.readFull(header[:]); err != nil {
			return p, err
		}
		e := packEntry{id: ID{Kind: Kind(header[0])}, codec: header[33], raw: binary.BigEndian.Uint64(header[34:42]), stored: binary.BigEndian.Uint64(header[42:]), offset: offset + recordHeaderSize}
		copy(e.id.Digest[:], header[1:33])
		if !e.id.Kind.valid() || e.raw < HeaderSize {
			return p, invalid("pack record type/length")
		}
		if _, err = Header(e.id.Kind, e.raw-HeaderSize); err != nil {
			return p, err
		}
		if e.offset > info.Size()-32 || e.stored == 0 || e.stored > e.raw+65536 || e.stored > uint64(info.Size()-32-e.offset) || e.codec > 1 || e.codec == 0 && e.raw != e.stored {
			return p, invalid("pack record bounds/codec")
		}
		if i > 0 && !idLess(p.entries[len(p.entries)-1].id, e.id) {
			return p, invalid("pack duplicate/order")
		}
		if _, err = io.CopyBuffer(io.Discard, io.LimitReader(r, int64(e.stored)), buffer); err != nil {
			return p, err
		}
		offset = e.offset + int64(e.stored)
		p.entries = append(p.entries, e)
	}
	if offset != info.Size()-32 {
		return p, invalid("pack trailing/truncated")
	}
	var footer [32]byte
	if _, err = f.ReadAt(footer[:], offset); err != nil {
		return p, err
	}
	if !bytes.Equal(h.Sum(nil), footer[:]) || hex.EncodeToString(footer[:]) != name {
		return p, invalid("pack checksum/address")
	}
	if validate {
		for _, e := range p.entries {
			if _, err = inspectRecord(ctx, f, e, nil, nil, buffer, nil); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}
func inspectRecord(ctx context.Context, f *os.File, e packEntry, wire, payload io.Writer, buffer []byte, budget *uint64) (*node, error) {
	section := io.NewSectionReader(f, e.offset, int64(e.stored))
	if e.codec == 0 {
		return inspect(ctx, e.id, section, wire, payload, buffer, budget)
	}
	// ReadByte prevents zlib from consuming bytes beyond its own stream.
	// Buffered read-ahead remains visible to the exact section-EOF check.
	source := bufio.NewReaderSize(section, 32<<10)
	compressed, err := zlib.NewReader(source)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	counted := &countReader{reader: io.LimitReader(compressed, int64(e.raw)+1)}
	n, err := inspect(ctx, e.id, counted, wire, payload, buffer, budget)
	if err != nil {
		return nil, err
	}
	if _, trailing := source.ReadByte(); counted.n != e.raw || trailing != io.EOF {
		return nil, invalid("pack raw length/compressed trailing")
	}
	if err = compressed.Close(); err != nil {
		return nil, err
	}
	return n, nil
}

type countReader struct {
	reader io.Reader
	n      uint64
}

func (r *countReader) Read(b []byte) (int, error) {
	n, err := r.reader.Read(b)
	r.n += uint64(n)
	return n, err
}

func indexBytes(p packInfo) []byte {
	b := make([]byte, 12+32+len(p.entries)*58)
	copy(b, indexMagic)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(p.entries)))
	digest, _ := hex.DecodeString(p.name)
	copy(b[12:44], digest)
	for i, e := range p.entries {
		out := b[44+i*58 : 44+(i+1)*58]
		out[0] = byte(e.id.Kind)
		copy(out[1:33], e.id.Digest[:])
		out[33] = e.codec
		binary.BigEndian.PutUint64(out[34:42], e.raw)
		binary.BigEndian.PutUint64(out[42:50], e.stored)
		binary.BigEndian.PutUint64(out[50:], uint64(e.offset))
	}
	return checkedBytes(b)
}
func matchesIndex(p packInfo, b []byte) bool {
	if len(b) != 44+58*len(p.entries)+32 || string(b[:8]) != indexMagic ||
		binary.BigEndian.Uint32(b[8:12]) != uint32(len(p.entries)) || !validChecksum(b) {
		return false
	}
	digest, _ := hex.DecodeString(p.name)
	if !bytes.Equal(b[12:44], digest) {
		return false
	}
	for i, e := range p.entries {
		in := b[44+i*58 : 44+(i+1)*58]
		if Kind(in[0]) != e.id.Kind || !bytes.Equal(in[1:33], e.id.Digest[:]) || in[33] != e.codec ||
			binary.BigEndian.Uint64(in[34:42]) != e.raw || binary.BigEndian.Uint64(in[42:50]) != e.stored ||
			binary.BigEndian.Uint64(in[50:]) != uint64(e.offset) {
			return false
		}
	}
	return true
}
func (s *Store) readPack(ctx context.Context, name string, buffer []byte) (packInfo, error) {
	p, err := s.scanPack(ctx, name, false, buffer)
	if err != nil {
		return p, err
	}
	b, err := readBoundedFile(s.packPath(name, ".idx"), 44+58*MaxPackObjects+32)
	if err == nil && matchesIndex(p, b) {
		return p, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrInvalidObject) {
		return p, err
	}
	return s.scanPack(ctx, name, true, buffer)
}
func (s *Store) reaffirmPacks(ctx context.Context, buffer []byte) error {
	names, err := s.packNames(ctx, true)
	if err != nil {
		return storageError("pack catalog durability", err)
	}
	if len(names) == 0 {
		return nil
	}
	return s.rebuildPacks(ctx, buffer)
}
func (s *Store) packed(ctx context.Context, id ID, wire, payload io.Writer, buffer []byte, budget *uint64) (*node, bool, error) {
	names, err := s.packNames(ctx, true)
	if err != nil {
		return nil, false, err
	}
	var result *node
	found, total := false, 0
	for _, name := range names {
		p, err := s.readPack(ctx, name, buffer)
		if err != nil {
			return nil, false, err
		}
		total += len(p.entries)
		if total > MaxPackObjects {
			return nil, false, ErrGraphLimit
		}
		i := sort.Search(len(p.entries), func(i int) bool { return !idLess(p.entries[i].id, id) })
		if i == len(p.entries) || p.entries[i].id != id {
			continue
		}
		f, err := regular(s.packPath(name, ".pack"))
		if err != nil {
			return nil, true, err
		}
		n, err := inspectRecord(ctx, f, p.entries[i], wire, payload, buffer, budget)
		if err = errors.Join(err, f.Close()); err != nil {
			return nil, true, err
		}
		if !found {
			result, found = n, true
			wire, payload = nil, nil
		}
	}
	return result, found, nil
}

// Pack explicitly publishes compressed physical copies of selected objects. It
// never removes loose data, changes logical IDs, or updates refs. Duplicate input
// IDs are rejected; retries of the same set are deterministic and idempotent.
func (s *Store) Pack(ctx context.Context, ids []ID) (name string, err error) {
	if len(ids) == 0 || len(ids) > MaxPackObjects {
		return "", invalid("pack selection count")
	}
	ids = append([]ID(nil), ids...)
	sort.Slice(ids, func(i, j int) bool { return idLess(ids[i], ids[j]) })
	for i, id := range ids {
		if !id.Kind.valid() || i > 0 && ids[i-1] == id {
			return "", invalid("pack selection type/duplicate")
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", err
	}
	defer release(&err, unlock)
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	// Validate all existing physical packs before acknowledging another write.
	if _, err = s.validPacks(ctx, buffer[:]); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(s.meta, tempPrefix)
	if err != nil {
		return "", err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	h := sha256.New()
	w := io.MultiWriter(f, h)
	var head [12]byte
	copy(head[:], packMagic)
	binary.BigEndian.PutUint32(head[8:], uint32(len(ids)))
	if err = writeAll(w, head[:]); err != nil {
		return "", err
	}
	size := int64(12)
	var raw, compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	defer z.Close()
	for _, id := range ids {
		raw.Reset()
		if _, err = s.load(ctx, id, &raw, nil, buffer[:], nil); err != nil {
			return "", err
		}
		compressed.Reset()
		z.Reset(&compressed)
		if err = writeAll(z, raw.Bytes()); err != nil {
			return "", err
		}
		if err = z.Close(); err != nil {
			return "", err
		}
		data := raw.Bytes()
		codec := byte(0)
		if compressed.Len() < raw.Len() {
			data = compressed.Bytes()
			codec = 1
		}
		size += recordHeaderSize + int64(len(data))
		if size+32 > MaxPackBytes {
			return "", ErrGraphLimit
		}
		var rh [recordHeaderSize]byte
		rh[0] = byte(id.Kind)
		copy(rh[1:33], id.Digest[:])
		rh[33] = codec
		binary.BigEndian.PutUint64(rh[34:42], uint64(raw.Len()))
		binary.BigEndian.PutUint64(rh[42:], uint64(len(data)))
		if err = writeAll(w, rh[:]); err != nil {
			return "", err
		}
		if err = writeAll(w, data); err != nil {
			return "", err
		}
	}
	digest := h.Sum(nil)
	name = hex.EncodeToString(digest)
	if err = writeAll(f, digest); err != nil {
		return "", err
	}
	names, e := s.packNames(ctx, false)
	if e != nil {
		return name, e
	}
	at := sort.SearchStrings(names, name)
	if at == len(names) || names[at] != name {
		if len(names) >= MaxPacks {
			return name, ErrGraphLimit
		}
		total := len(ids)
		for _, old := range names {
			p, e := s.scanPack(ctx, old, false, buffer[:])
			if e != nil {
				return name, e
			}
			total += len(p.entries)
		}
		if total > MaxPackObjects {
			return name, ErrGraphLimit
		}
	}
	if err = s.finish(ctx, f, s.packPath(name, ".pack")); err != nil {
		return name, err
	}
	// The complete pack is now visible. A crash before index/catalog publication
	// leaves a recoverable pack, never an index referring to partial pack bytes.
	err = s.rebuildPacks(ctx, buffer[:])
	return name, err
}
func (s *Store) validPacks(ctx context.Context, buffer []byte) ([]packInfo, error) {
	names, err := s.packNames(ctx, false)
	if err != nil {
		return nil, err
	}
	packs := make([]packInfo, 0, len(names))
	total := 0
	for _, name := range names {
		p, err := s.scanPack(ctx, name, true, buffer)
		if err != nil {
			return nil, err
		}
		total += len(p.entries)
		if total > MaxPackObjects {
			return nil, ErrGraphLimit
		}
		packs = append(packs, p)
	}
	return packs, nil
}

// RebuildPackIndexes verifies all intact packs before replacing any derived
// metadata. It also recovers packs published before their index/catalog. Opening
// and reading never invoke this write operation or remove a stale writer lock.
func (s *Store) RebuildPackIndexes(ctx context.Context) (err error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release(&err, unlock)
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	return s.rebuildPacks(ctx, buffer[:])
}
func (s *Store) rebuildPacks(ctx context.Context, buffer []byte) error {
	packs, err := s.validPacks(ctx, buffer)
	if err != nil {
		return err
	}
	for _, p := range packs {
		if err = s.republishPack(ctx, p.name, buffer); err != nil {
			return err
		}
		if err = s.writeFile(ctx, s.packPath(p.name, ".idx"), indexBytes(p)); err != nil {
			return err
		}
	}
	catalog := make([]byte, 12+32*len(packs))
	copy(catalog, catalogMagic)
	binary.BigEndian.PutUint32(catalog[8:12], uint32(len(packs)))
	for i, p := range packs {
		digest, _ := hex.DecodeString(p.name)
		copy(catalog[12+i*32:], digest)
	}
	if err = s.writeFile(ctx, s.packPath("catalog", ""), checkedBytes(catalog)); err != nil {
		return err
	}
	for _, p := range packs {
		for _, e := range p.entries {
			s.rememberDurable(e.id)
		}
	}
	return nil
}
func (s *Store) republishPack(ctx context.Context, name string, buffer []byte) (err error) {
	source, err := regular(s.packPath(name, ".pack"))
	if err != nil {
		return err
	}
	defer source.Close()
	f, err := os.CreateTemp(s.meta, tempPrefix)
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if _, err = io.CopyBuffer(f, &contextReader{ctx: ctx, reader: source}, buffer); err != nil {
		return err
	}
	if err = source.Close(); err != nil {
		return err
	}
	return s.finish(ctx, f, s.packPath(name, ".pack"))
}

// reaffirmPacked establishes actual pack, index and catalog barriers rather than
// creating loose fallback copies. Called only under the existing writer lock.
func (s *Store) reaffirmPacked(ctx context.Context, id ID, buffer []byte, budget *uint64) error {
	_, found, err := s.packed(ctx, id, nil, nil, buffer, budget)
	if err != nil {
		return corrupt(id, err)
	}
	if !found {
		return corrupt(id, os.ErrNotExist)
	}
	if err = s.rebuildPacks(ctx, buffer); err != nil {
		return storageError("packed closure durability", err)
	}
	return nil
}

// Keep physical diagnostics distinct from canonical object addresses.
func packError(err error) error { return storageError("packs", err) }
