package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Repository represents a Pebble repository
type Repository struct {
	rootPath string
	objectsPath string
	refsPath string
	indexPath string
	headPath string
}

// NewRepository creates or opens a repository
func NewRepository(rootPath string) (*Repository, error) {
	repo := &Repository{
		rootPath: rootPath,
		objectsPath: filepath.Join(rootPath, ".pebble", "objects"),
		refsPath: filepath.Join(rootPath, ".pebble", "refs"),
		indexPath: filepath.Join(rootPath, ".pebble", "index"),
		headPath: filepath.Join(rootPath, ".pebble", "HEAD"),
	}
	
	// Create directories if they don't exist
	if err := os.MkdirAll(repo.objectsPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create objects directory: %w", err)
	}
	if err := os.MkdirAll(repo.refsPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create refs directory: %w", err)
	}
	
	return repo, nil
}

// Init initializes a new repository
func Init(rootPath string) (*Repository, error) {
	repo, err := NewRepository(rootPath)
	if err != nil {
		return nil, err
	}
	
	// Write initial HEAD
	if err := os.WriteFile(repo.headPath, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		return nil, fmt.Errorf("failed to write HEAD: %w", err)
	}
	
	// Create initial branch
	if err := os.MkdirAll(filepath.Join(repo.refsPath, "heads"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create refs/heads: %w", err)
	}
	
	// Initialize main branch as empty
	if err := os.WriteFile(filepath.Join(repo.refsPath, "heads", "main"), []byte("\n"), 0644); err != nil {
		return nil, fmt.Errorf("failed to create main branch: %w", err)
	}
	
	return repo, nil
}

// RootPath returns the repository root path
func (r *Repository) RootPath() string {
	return r.rootPath
}

// ObjectsPath returns the objects directory path
func (r *Repository) ObjectsPath() string {
	return r.objectsPath
}

// GetHead returns the current HEAD reference (e.g., "heads/main")
func (r *Repository) GetHead() (string, error) {
	data, err := os.ReadFile(r.headPath)
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD: %w", err)
	}
	
	content := string(data)
	// Trim trailing whitespace (handles both \n and \r\n)
	for len(content) > 0 && (content[len(content)-1] == '\n' || content[len(content)-1] == '\r') {
		content = content[:len(content)-1]
	}
	
	if len(content) > 10 && content[:10] == "ref: refs/" {
		// "ref: refs/" = 10 chars, then we want content after that = position 10
		return content[10:], nil
	}
	
	return "", fmt.Errorf("invalid HEAD: %s", content)
}

// SetHead sets the HEAD to a reference
func (r *Repository) SetHead(ref string) error {
	data := []byte(fmt.Sprintf("ref: refs/%s\n", ref))
	return os.WriteFile(r.headPath, data, 0644)
}

// GetRef returns the commit hash for a reference
func (r *Repository) GetRef(ref string) (string, error) {
	refPath := filepath.Join(r.refsPath, ref)
	data, err := os.ReadFile(refPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read ref %s: %w", ref, err)
	}
	
	hash := string(data)
	hash = hash[:len(hash)-1] // Remove newline
	
	return hash, nil
}

// SetRef sets a reference to a commit hash
func (r *Repository) SetRef(ref, hash string) error {
	refPath := filepath.Join(r.refsPath, ref)
	
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(refPath), 0755); err != nil {
		return fmt.Errorf("failed to create ref directory: %w", err)
	}
	
	return os.WriteFile(refPath, []byte(hash+"\n"), 0644)
}

// StoreObject stores an object in the repository
func (r *Repository) StoreObject(obj *Object) error {
	// Store in subdirectory based on first 2 chars of hash
	hash := obj.Hash
	subDir := hash[:2]
	objDir := filepath.Join(r.objectsPath, subDir)
	
	if err := os.MkdirAll(objDir, 0755); err != nil {
		return fmt.Errorf("failed to create object directory: %w", err)
	}
	
	objPath := filepath.Join(objDir, hash[2:])
	
	// Check if already exists
	if _, err := os.Stat(objPath); err == nil {
		return nil // Already exists
	}
	
	// Store object metadata (not raw content)
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}
	
	return os.WriteFile(objPath, data, 0644)
}

// GetObject retrieves an object by hash
func (r *Repository) GetObject(hash string) (*Object, error) {
	if len(hash) < 2 {
		return nil, fmt.Errorf("invalid hash: %s", hash)
	}
	
	subDir := hash[:2]
	objPath := filepath.Join(r.objectsPath, subDir, hash[2:])
	
	data, err := os.ReadFile(objPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read object: %w", err)
	}
	
	var obj Object
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal object: %w", err)
	}
	
	return &obj, nil
}

// GetCommit retrieves a commit object by hash
func (r *Repository) GetCommit(hash string) (*Commit, error) {
	obj, err := r.GetObject(hash)
	if err != nil {
		return nil, err
	}

	// Object stores Commit in Content as JSON
	var commit Commit
	if err := json.Unmarshal(obj.Content, &commit); err != nil {
		return nil, fmt.Errorf("failed to unmarshal commit: %w", err)
	}

	return &commit, nil
}

// SaveIndex saves the current index
func (r *Repository) SaveIndex(index *Index) error {
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}
	
	return os.WriteFile(r.indexPath, data, 0644)
}

// LoadIndex loads the current index
func (r *Repository) LoadIndex() (*Index, error) {
	data, err := os.ReadFile(r.indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read index: %w", err)
	}
	
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("failed to unmarshal index: %w", err)
	}
	
	return &index, nil
}

// IsInitialized checks if the directory is a Pebble repository
func IsInitialized(rootPath string) bool {
	pebbleDir := filepath.Join(rootPath, ".pebble")
	info, err := os.Stat(pebbleDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}
