package snapshot

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Repository struct{ rootPath, objectsPath, refsPath, indexPath, headPath string }

func repositoryAt(root string) *Repository {
	return &Repository{root, filepath.Join(root, ".pebble", "objects"), filepath.Join(root, ".pebble", "refs"), filepath.Join(root, ".pebble", "index"), filepath.Join(root, ".pebble", "HEAD")}
}

// NewRepository opens an existing repository, searching ancestors without creating files.
func NewRepository(path string) (*Repository, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for {
		if IsInitialized(root) {
			return repositoryAt(root), nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, fmt.Errorf("not a Pebble repository (run pebble init)")
		}
		root = parent
	}
}

func Init(path string) (*Repository, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	// Never reset an existing repository, including a partially initialized one.
	if err = os.Mkdir(filepath.Join(root, ".pebble"), 0755); err != nil {
		return nil, fmt.Errorf("cannot initialize repository: %w", err)
	}
	r := repositoryAt(root)
	if err = os.MkdirAll(r.objectsPath, 0755); err != nil {
		return nil, err
	}
	if err = r.SetRef("heads/main", ""); err != nil {
		return nil, err
	}
	if err = r.SetHead("heads/main"); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Repository) RootPath() string    { return r.rootPath }
func (r *Repository) ObjectsPath() string { return r.objectsPath }

// atomicWrite keeps readers from observing truncated metadata or objects.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pebble-tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Lock serializes cooperating writers. A stale lock is deliberately not removed automatically.
func (r *Repository) Lock() (func(), error) {
	path := filepath.Join(r.rootPath, ".pebble", "write.lock")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("repository locked or not writable (inspect %s): %w", path, err)
	}
	if _, err = fmt.Fprintf(f, "pid %d\n", os.Getpid()); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err = f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}
func validHash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == 32 && hash == strings.ToLower(hash)
}
func validateRef(ref string) error {
	if !strings.HasPrefix(ref, "heads/") {
		return fmt.Errorf("invalid branch reference %q", ref)
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("invalid branch reference %q", ref)
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return fmt.Errorf("invalid branch reference %q", ref)
			}
		}
	}
	return validFilePath(ref)
}
func (r *Repository) GetHead() (string, error) {
	data, err := os.ReadFile(r.headPath)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, "ref: refs/") {
		return "", fmt.Errorf("invalid HEAD")
	}
	ref := strings.TrimPrefix(s, "ref: refs/")
	return ref, validateRef(ref)
}
func (r *Repository) SetHead(ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	return atomicWrite(r.headPath, []byte("ref: refs/"+ref+"\n"), 0644)
}
func (r *Repository) GetRef(ref string) (string, error) {
	if err := validateRef(ref); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(r.refsPath, filepath.FromSlash(ref)))
	if err != nil {
		return "", err
	}
	hash := strings.TrimSpace(string(data))
	if hash != "" && !validHash(hash) {
		return "", fmt.Errorf("invalid hash in ref %s", ref)
	}
	return hash, nil
}
func (r *Repository) SetRef(ref, hash string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	if hash != "" && !validHash(hash) {
		return fmt.Errorf("invalid hash %q", hash)
	}
	path := filepath.Join(r.refsPath, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return atomicWrite(path, []byte(hash+"\n"), 0644)
}
func (r *Repository) GetRefs() ([]Ref, error) {
	refs := []Ref{}
	base := filepath.Join(r.refsPath, "heads")
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || strings.HasPrefix(info.Name(), ".pebble-tmp-") {
			return nil
		}
		rel, err := filepath.Rel(r.refsPath, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		hash, err := r.GetRef(name)
		if err != nil {
			return err
		}
		refs = append(refs, Ref{name, hash})
		return nil
	})
	return refs, err
}
func validateObject(obj *Object) error {
	if obj == nil {
		return fmt.Errorf("nil object")
	}
	switch obj.Type {
	case ObjectTypeBlob, ObjectTypeTree, ObjectTypeCommit, ObjectTypeIndex:
	default:
		return fmt.Errorf("invalid object type %q", obj.Type)
	}
	if !validHash(obj.Hash) || ComputeHash(obj.Content) != obj.Hash || obj.Size != int64(len(obj.Content)) {
		return fmt.Errorf("object integrity check failed for %s", obj.Hash)
	}
	return nil
}
func (r *Repository) StoreObject(obj *Object) error {
	if err := validateObject(obj); err != nil {
		return err
	}
	path := filepath.Join(r.objectsPath, obj.Hash[:2], obj.Hash[2:])
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		existing, err := r.GetObject(obj.Hash)
		if err != nil {
			return err
		}
		if existing.Type != obj.Type {
			return fmt.Errorf("object type collision for %s", obj.Hash)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0644)
}
func (r *Repository) GetObject(hash string) (*Object, error) {
	if !validHash(hash) {
		return nil, fmt.Errorf("invalid hash %q", hash)
	}
	data, err := os.ReadFile(filepath.Join(r.objectsPath, hash[:2], hash[2:]))
	if err != nil {
		return nil, err
	}
	var obj Object
	if err = json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj.Hash != hash {
		return nil, fmt.Errorf("object address mismatch: %s", hash)
	}
	if err = validateObject(&obj); err != nil {
		return nil, err
	}
	return &obj, nil
}
func (r *Repository) GetCommit(hash string) (*Commit, error) {
	obj, err := r.GetObject(hash)
	if err != nil {
		return nil, err
	}
	if obj.Type != ObjectTypeCommit {
		return nil, fmt.Errorf("not a commit: %s", hash)
	}
	var commit Commit
	if err = json.Unmarshal(obj.Content, &commit); err != nil {
		return nil, err
	}
	commit.Hash = hash
	return &commit, nil
}
func (r *Repository) SaveIndex(index *Index) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return atomicWrite(r.indexPath, data, 0644)
}
func (r *Repository) LoadIndex() (*Index, error) {
	data, err := os.ReadFile(r.indexPath)
	if err != nil {
		return nil, err
	}
	var index Index
	err = json.Unmarshal(data, &index)
	return &index, err
}
func IsInitialized(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".pebble", "HEAD"))
	return err == nil && info.Mode().IsRegular()
}
