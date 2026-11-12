package storev2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	pe "github.com/suttonwilliamd/pebble/internal/errors"
	"github.com/suttonwilliamd/pebble/internal/processlock"
)

const formatMarker = "pebble-format 2\n"
const tempPrefix = ".pebble-tmp-"

var buffers = sync.Pool{New: func() any { return new([32 << 10]byte) }}

type Store struct {
	root string
	meta string
	// fault is private and instance-scoped. Tests inject failures at actual I/O
	// boundaries without replacing validation, locking, rename or durability.
	fault func(stage, path string) error
	// Only successful publication barriers are memoized, never validation.
	// The mutex also supplies Go-level synchronization between writer calls.
	durableMu sync.Mutex
	durable   map[ID]struct{}
}

func storageError(operation string, err error) error {
	return pe.Wrap(pe.ErrCodeStorageError, "v2 "+operation, err)
}
func corrupt(id ID, err error) error {
	return pe.Wrap(pe.ErrCodeObjectCorrupted, "v2 object "+id.String(), err)
}

func directory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not an ordinary directory: %s", path)
	}
	return nil
}
func regular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not an ordinary file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		if err == nil {
			err = fmt.Errorf("file changed while opening: %s", path)
		}
		return nil, err
	}
	return f, nil
}

func directoryEntries(path string, limit int) (_ []os.DirEntry, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	entries, err := f.ReadDir(limit)
	if err == io.EOF {
		err = nil
	}
	return entries, err
}

// Open opens exactly root, without ancestor discovery, creation or migration.
func Open(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Store{root: absolute, meta: filepath.Join(absolute, ".pebble")}
	if err = s.guard(); err != nil {
		return nil, err
	}
	if err = checkPlatform(absolute); err != nil {
		return nil, storageError("filesystem durability support", err)
	}
	return s, nil
}

// Init explicitly initializes an existing EMPTY directory. Existing v1, v2,
// partial repositories and user files are never upgraded, reset or removed.
// An interrupted initialization must be inspected manually; it is not repaired.
func Init(ctx context.Context, root string) (_ *Store, err error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = directory(absolute); err != nil {
		return nil, err
	}
	entries, err := directoryEntries(absolute, 1)
	if err != nil {
		return nil, err
	}
	if len(entries) != 0 {
		return nil, pe.AlreadyInitialized(absolute)
	}
	if err = checkPlatform(absolute); err != nil {
		return nil, storageError("filesystem durability support", err)
	}
	lockPath := filepath.Join(absolute, ".pebble-init.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, storageError("initialization lock", err)
	}
	defer func() { err = errors.Join(err, lock.Close(), os.Remove(lockPath)) }()
	entries, err = directoryEntries(absolute, 2)
	if err != nil {
		return nil, err
	}
	if len(entries) != 1 || entries[0].Name() != ".pebble-init.lock" {
		return nil, pe.AlreadyInitialized(absolute)
	}
	stage, err := os.MkdirTemp(absolute, ".pebble-init-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage) // Only our unpublished staging directory.
	s := &Store{root: absolute, meta: stage}
	for _, name := range []string{"objects", "refs"} {
		if err = makeDurableDirectory(filepath.Join(stage, name)); err != nil {
			return nil, storageError("initialize "+name, err)
		}
	}
	if err = s.writeFile(ctx, filepath.Join(stage, "format"), []byte(filesystemMarker)); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	final := filepath.Join(absolute, ".pebble")
	if _, err = os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = pe.AlreadyInitialized(absolute)
		}
		return nil, err
	}
	if err = publishDirectory(stage, final); err != nil {
		return nil, storageError("publish initialization (inspect directory before retry)", errors.Join(ErrPublicationUncertain, err))
	}
	return Open(absolute)
}

func (s *Store) guard() error {
	for _, path := range []string{s.root, s.meta} {
		if err := directory(path); err != nil {
			return pe.Wrap(pe.ErrCodeNotInitialized, "v2 repository", errors.Join(ErrFormat, err))
		}
	}
	if _, err := s.marker(); err != nil {
		return pe.Wrap(pe.ErrCodeNotInitialized, "v2 marker required; no implicit migration", errors.Join(ErrFormat, err))
	}
	for _, name := range []string{"objects", "refs"} {
		if err := directory(filepath.Join(s.meta, name)); err != nil {
			return pe.Wrap(pe.ErrCodeNotInitialized, "v2 "+name, errors.Join(ErrFormat, err))
		}
	}
	return nil
}

func (s *Store) lock(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.guard(); err != nil {
		return nil, err
	}
	// Legacy exclusive markers are never guessed stale. New writers use a
	// persistent OS guard whose ownership dies with the process.
	path := filepath.Join(s.meta, "write.lock")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, storageError("legacy writer lock (inspect before removal)", errors.Join(ErrLocked, err))
	}
	unlock, err := processlock.Acquire(filepath.Join(s.meta, "write.guard"))
	if err != nil {
		return nil, storageError("writer guard", errors.Join(ErrLocked, err))
	}
	if err = s.guard(); err != nil {
		return nil, errors.Join(err, unlock())
	}
	return unlock, nil
}
func release(err *error, unlock func() error) {
	if e := unlock(); e != nil {
		*err = errors.Join(*err, ErrPublicationUncertain, storageError("release writer lock", e))
	}
}

func (s *Store) objectPath(id ID) string {
	return filepath.Join(s.meta, "objects", fmt.Sprintf("%02x-%x.obj", byte(id.Kind), id.Digest))
}
func (s *Store) inject(stage, path string) error {
	if s.fault != nil {
		return s.fault(stage, path)
	}
	return nil
}

type contextReader struct {
	ctx     context.Context
	reader  io.Reader
	failure error
}

func (r *contextReader) Read(p []byte) (int, error) {
	if r.failure != nil {
		return 0, r.failure
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	// Exact-read helpers can suppress an error accompanying a full buffer.
	// A source failure is fatal even if the source later returns only EOF.
	if err != nil && err != io.EOF {
		r.failure = err
	}
	return n, err
}

func (r *contextReader) readFull(p []byte) (int, error) {
	n, err := io.ReadFull(r, p)
	if r.failure != nil {
		return n, r.failure
	}
	return n, err
}
func writeAll(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}

// inspect consumes exactly one bounded envelope. Outputs, when provided, are
// provisional until it returns nil. Chunk bytes stream through a reusable buffer.
func inspect(ctx context.Context, expected ID, source io.Reader, wireOut, payloadOut io.Writer, buffer []byte, budget *uint64) (*node, error) {
	if !expected.Kind.valid() {
		return nil, invalid("id-type")
	}
	r := &contextReader{ctx: ctx, reader: source}
	var header [HeaderSize]byte
	if _, err := r.readFull(header[:]); err != nil {
		return nil, errors.Join(invalid("truncated"), err)
	}
	if !bytes.Equal(header[:4], []byte("PB2\x00")) {
		return nil, invalid("version")
	}
	kind, size := Kind(header[4]), binary.BigEndian.Uint64(header[5:])
	if _, err := Header(kind, size); err != nil {
		return nil, err
	}
	if kind != expected.Kind {
		return nil, invalid("id-type")
	}
	if budget != nil {
		if size+HeaderSize > *budget {
			return nil, ErrGraphLimit
		}
		*budget -= size + HeaderSize
	}
	h := sha256.New()
	h.Write(header[:])
	if wireOut != nil {
		if err := writeAll(wireOut, header[:]); err != nil {
			return nil, err
		}
	}
	var n *node
	if kind == Chunk {
		writers := make([]io.Writer, 1, 3)
		writers[0] = h
		if wireOut != nil {
			writers = append(writers, wireOut)
		}
		if payloadOut != nil {
			writers = append(writers, payloadOut)
		}
		copied, err := io.CopyBuffer(io.MultiWriter(writers...), io.LimitReader(r, int64(size)), buffer)
		if err != nil {
			return nil, err
		}
		if uint64(copied) != size {
			return nil, invalid("truncated")
		}
		n = &node{kind: kind, size: size}
	} else {
		payload := make([]byte, int(size))
		if _, err := r.readFull(payload); err != nil {
			return nil, errors.Join(invalid("truncated"), err)
		}
		h.Write(payload)
		var err error
		if n, err = parsePayload(kind, payload); err != nil {
			return nil, err
		}
		if wireOut != nil {
			if err = writeAll(wireOut, payload); err != nil {
				return nil, err
			}
		}
	}
	var extra [1]byte
	if count, err := r.readFull(extra[:]); count != 0 || err != io.EOF {
		if count != 0 {
			return nil, invalid("trailing")
		}
		return nil, err
	}
	var digest [32]byte
	h.Sum(digest[:0])
	if digest != expected.Digest {
		return nil, invalid("address")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) load(ctx context.Context, id ID, wireOut, payloadOut io.Writer, buffer []byte, budget *uint64) (_ *node, err error) {
	if !id.Kind.valid() {
		return nil, corrupt(id, invalid("id-type"))
	}
	f, err := regular(s.objectPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			n, found, packErr := s.packed(ctx, id, wireOut, payloadOut, buffer, budget)
			if packErr != nil {
				return nil, corrupt(id, packErr)
			}
			if found {
				return n, nil
			}
			return nil, pe.Wrap(pe.ErrCodeObjectNotFound, "v2 object "+id.String(), err)
		}
		return nil, corrupt(id, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	// Validate packed copies too: a good loose representation must not mask a
	// corrupt published pack or a misaddressed duplicate.
	if _, _, err = s.packed(ctx, id, nil, nil, buffer, budget); err != nil {
		return nil, corrupt(id, err)
	}
	n, err := inspect(ctx, id, f, wireOut, payloadOut, buffer, budget)
	if err != nil {
		return nil, corrupt(id, err)
	}
	return n, nil
}

// Put accepts one complete canonical envelope at its expected typed address.
// Dependencies may be absent until Verify/UpdateRef; no ref is changed here.
func (s *Store) Put(ctx context.Context, id ID, source io.Reader) (err error) {
	if !id.Kind.valid() {
		return corrupt(id, invalid("id-type"))
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release(&err, unlock)
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	path := s.objectPath(id)
	if _, err = os.Lstat(path); err == nil {
		if _, err = s.load(ctx, id, nil, nil, buffer[:], nil); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return storageError("inspect existing object", err)
	} else {
		if _, _, err = s.packed(ctx, id, nil, nil, buffer[:], nil); err != nil {
			return corrupt(id, err)
		}
	}
	f, err := os.CreateTemp(filepath.Join(s.meta, "objects"), tempPrefix)
	if err != nil {
		return storageError("stage object", err)
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	n, err := inspect(ctx, id, source, f, nil, buffer[:], nil)
	if err != nil {
		return corrupt(id, err)
	}
	if n.symlinks {
		if err = s.requireFilesystem(); err != nil {
			return err
		}
	}
	// Even an identical retry is republished after verifying existing bytes.
	// This establishes a fresh namespace barrier after an uncertain earlier I/O.
	if err = s.finish(ctx, f, path); err != nil {
		return err
	}
	s.rememberDurable(id)
	return nil
}

// Copy streams the canonical envelope to dst, validating its address and schema.
// Bytes delivered to dst are provisional: callers must discard them on error.
func (s *Store) Copy(ctx context.Context, id ID, dst io.Writer) error {
	if err := s.guard(); err != nil {
		return err
	}
	buffer := buffers.Get().(*[32 << 10]byte)
	defer buffers.Put(buffer)
	_, err := s.load(ctx, id, dst, nil, buffer[:], nil)
	return err
}

func (s *Store) finish(ctx context.Context, f *os.File, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.inject("file-sync", path); err != nil {
		return storageError("file sync", err)
	}
	if err := f.Sync(); err != nil {
		return storageError("file sync", err)
	}
	if err := s.inject("close", path); err != nil {
		return storageError("close staged file", err)
	}
	if err := f.Close(); err != nil {
		return storageError("close staged file", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.inject("before-publish", path); err != nil {
		return storageError("before publication", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	published, err := publishFile(f.Name(), path, s.fault)
	if err != nil {
		if published {
			err = errors.Join(ErrPublicationUncertain, err)
		}
		return storageError("publish "+path, err)
	}
	if err = s.inject("after-publish", path); err != nil {
		return storageError("publication acknowledgement", errors.Join(ErrPublicationUncertain, err))
	}
	return nil
}
func (s *Store) writeFile(ctx context.Context, path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), tempPrefix)
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if err = writeAll(f, data); err != nil {
		return err
	}
	return s.finish(ctx, f, path)
}

func (s *Store) knownDurable(id ID) bool {
	s.durableMu.Lock()
	defer s.durableMu.Unlock()
	_, ok := s.durable[id]
	return ok
}

func (s *Store) rememberDurable(id ID) {
	s.durableMu.Lock()
	defer s.durableMu.Unlock()
	if _, ok := s.durable[id]; ok {
		return
	}
	if s.durable == nil || len(s.durable) >= MaxGraphObjects {
		s.durable = make(map[ID]struct{})
	}
	s.durable[id] = struct{}{}
}

// reaffirm is called under the writer lock for closure objects whose completed
// namespace barrier this handle has not observed. Cold/reopened repositories
// cannot infer durability from valid visible bytes after an uncertain operation.
func (s *Store) reaffirm(ctx context.Context, id ID, buffer []byte, budget *uint64) error {
	source, err := regular(s.objectPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.reaffirmPacked(ctx, id, buffer, budget)
		}
		return corrupt(id, err)
	}
	defer source.Close()
	f, err := os.CreateTemp(filepath.Join(s.meta, "objects"), tempPrefix)
	if err != nil {
		return storageError("stage closure durability", err)
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if _, err = inspect(ctx, id, source, f, nil, buffer, budget); err != nil {
		return corrupt(id, err)
	}
	// Close the source before replacement, including on Windows handles that
	// do not grant delete sharing. No object name is ever temporarily removed.
	if err = source.Close(); err != nil {
		return storageError("close closure source", err)
	}
	if err = s.finish(ctx, f, s.objectPath(id)); err != nil {
		return err
	}
	s.rememberDurable(id)
	return nil
}
