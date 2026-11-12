package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// V1 stores a complete base64 JSON envelope, not streaming chunks. Refuse
// envelopes beyond this local resource policy instead of truncating history.
const MaxV1EnvelopeBytes = 128 << 20

type v1Source struct {
	root   string
	reads  map[string][32]byte
	budget uint64
}

// exactDirectory rejects symlinks/junctions and filesystem spelling aliases at
// every existing component. No discovery or legacy constructor is invoked.
func exactDirectory(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		children, err := os.ReadDir(current)
		if err != nil {
			return "", err
		}
		exact := false
		for _, child := range children {
			if child.Name() == part {
				exact = true
				break
			}
		}
		if !exact {
			return "", fmt.Errorf("filesystem alias in %s", path)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("not a real directory: %s", current)
		}
	}
	return path, nil
}

func (s *v1Source) read(ctx context.Context, relative string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(s.root, filepath.FromSlash(relative))
	if _, err := exactDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit || uint64(info.Size()) > s.budget {
		return nil, fmt.Errorf("invalid or oversized v1 file %s (limit %d)", relative, limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, fmt.Errorf("source changed opening %s", relative)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.ModTime() != after.ModTime() || opened.Mode() != after.Mode() {
		return nil, fmt.Errorf("source changed reading %s", relative)
	}
	if int64(len(data)) > limit || uint64(len(data)) > s.budget {
		return nil, fmt.Errorf("v1 read resource limit")
	}
	s.budget -= uint64(len(data))
	sum := sha256.Sum256(data)
	if previous, ok := s.reads[relative]; ok && previous != sum {
		return nil, fmt.Errorf("source changed: %s", relative)
	}
	s.reads[relative] = sum
	return data, nil
}

func v1Hash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == 32 && hash == strings.ToLower(hash)
}

// The shipped writer uses encoding/json on these exact structs. Roundtripping
// rejects unknown/duplicate fields, invalid UTF-8, and alternative wire schemas;
// external whitespace is harmless. No data is normalized into a different ID.
func decodeV1(data []byte, value any) error {
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(data), canonical) {
		return fmt.Errorf("unsupported/noncanonical v1 JSON metadata")
	}
	return nil
}

func (s *v1Source) object(ctx context.Context, hash string, kind snapshot.ObjectType) (*snapshot.Object, error) {
	if !v1Hash(hash) {
		return nil, fmt.Errorf("invalid v1 address %q", hash)
	}
	data, err := s.read(ctx, ".pebble/objects/"+hash[:2]+"/"+hash[2:], MaxV1EnvelopeBytes)
	if err != nil {
		return nil, err
	}
	var obj snapshot.Object
	if err = decodeV1(data, &obj); err != nil {
		return nil, err
	}
	if obj.Hash != hash || obj.Type != kind || obj.Size != int64(len(obj.Content)) || snapshot.ComputeHash(obj.Content) != hash {
		return nil, fmt.Errorf("corrupt v1 %s object %s", kind, hash)
	}
	return &obj, nil
}

func (s *v1Source) refs(ctx context.Context) (map[string]string, string, error) {
	for _, dir := range []string{".pebble", ".pebble/objects", ".pebble/refs", ".pebble/refs/heads"} {
		if _, err := exactDirectory(filepath.Join(s.root, filepath.FromSlash(dir))); err != nil {
			return nil, "", err
		}
	}
	if _, err := os.Lstat(filepath.Join(s.root, ".pebble", "format")); !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("migration requires unmarked v1 source")
	}
	for _, name := range []string{"write.lock", "worktree.lock"} {
		if _, err := os.Lstat(filepath.Join(s.root, ".pebble", name)); !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("source locked or inaccessible: %s; no lock was removed", name)
		}
	}
	data, err := s.read(ctx, ".pebble/HEAD", 8192)
	if err != nil {
		return nil, "", err
	}
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, "ref: refs/heads/") {
		return nil, "", fmt.Errorf("unsupported v1 HEAD")
	}
	head := strings.TrimPrefix(text, "ref: refs/")
	if !storev2.ValidBranch(strings.TrimPrefix(head, "heads/")) {
		return nil, "", fmt.Errorf("invalid v1 HEAD")
	}
	refs := map[string]string{}
	base := filepath.Join(s.root, ".pebble", "refs")
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in v1 refs: %s", path)
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == "." {
			return nil
		}
		if name != "heads" && !strings.HasPrefix(name, "heads/") {
			return fmt.Errorf("unsupported v1 ref namespace %s", name)
		}
		if entry.IsDir() {
			return nil
		}
		if !storev2.ValidBranch(strings.TrimPrefix(name, "heads/")) {
			return fmt.Errorf("unrepresentable v1 branch %s", name)
		}
		if len(refs) >= storev2.MaxGraphObjects {
			return fmt.Errorf("v1 ref resource limit")
		}
		data, err := s.read(ctx, ".pebble/refs/"+name, 8192)
		if err != nil {
			return err
		}
		hash := strings.TrimSpace(string(data))
		if hash != "" && !v1Hash(hash) {
			return fmt.Errorf("invalid v1 ref %s", name)
		}
		refs[name] = hash
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if _, ok := refs[head]; !ok {
		return nil, "", fmt.Errorf("v1 HEAD branch missing")
	}
	return refs, head, nil
}

func sortedV1Refs(refs map[string]string) []string {
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
