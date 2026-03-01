package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Object represents a stored object in the database
type Object struct {
	Hash      string    `json:"hash"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	RefCount  int       `json:"ref_count"`
}

// Snapshot represents a snapshot record
type Snapshot struct {
	Hash      string    `json:"hash"`
	TreeHash  string    `json:"tree_hash"`
	Message   string    `json:"message"`
	Author    string    `json:"author"`
	Email     string    `json:"email"`
	Timestamp time.Time `json:"timestamp"`
	ParentHash string   `json:"parent_hash,omitempty"`
}

// Ref represents a reference (branch/tag)
type Ref struct {
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Storage is the main storage interface
type Storage struct {
	rootPath   string
	objectsDir string
	dbPath     string
	mu         sync.RWMutex
	objects   map[string]Object
	snapshots map[string]Snapshot
	refs      map[string]Ref
}

// NewStorage creates a new storage instance
func NewStorage(rootPath string) (*Storage, error) {
	pebbleDir := filepath.Join(rootPath, ".pebble")
	objectsDir := filepath.Join(pebbleDir, "objects")
	dbPath := filepath.Join(pebbleDir, "storage.json")
	
	for _, dir := range []string{pebbleDir, objectsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create dir: %w", err)
		}
	}
	
	storage := &Storage{
		rootPath:   rootPath,
		objectsDir: objectsDir,
		dbPath:     dbPath,
		objects:   make(map[string]Object),
		snapshots: make(map[string]Snapshot),
		refs:      make(map[string]Ref),
	}
	
	// Load existing data
	storage.load()
	
	return storage, nil
}

type storageData struct {
	Objects   map[string]Object   `json:"objects"`
	Snapshots map[string]Snapshot `json:"snapshots"`
	Refs      map[string]Ref     `json:"refs"`
}

func (s *Storage) load() {
	if data, err := os.ReadFile(s.dbPath); err == nil {
		var sd storageData
		if json.Unmarshal(data, &sd) == nil {
			s.objects = sd.Objects
			s.snapshots = sd.Snapshots
			s.refs = sd.Refs
		}
	}
}

func (s *Storage) save() error {
	data := storageData{
		Objects:   s.objects,
		Snapshots: s.snapshots,
		Refs:      s.refs,
	}
	
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.dbPath, jsonData, 0644)
}

// StoreObject stores an object
func (s *Storage) StoreObject(hash, objType string, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.objects[hash] = Object{
		Hash:      hash,
		Type:      objType,
		Size:      size,
		CreatedAt: time.Now(),
		RefCount:  1,
	}
	return s.save()
}

// GetObject retrieves an object
func (s *Storage) GetObject(hash string) (*Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if obj, exists := s.objects[hash]; exists {
		return &obj, nil
	}
	return nil, fmt.Errorf("object not found: %s", hash)
}

// IncrementRefCount increments the reference count
func (s *Storage) IncrementRefCount(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if obj, exists := s.objects[hash]; exists {
		obj.RefCount++
		s.objects[hash] = obj
		return s.save()
	}
	return nil
}

// DecrementRefCount decrements the reference count
func (s *Storage) DecrementRefCount(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if obj, exists := s.objects[hash]; exists {
		obj.RefCount--
		s.objects[hash] = obj
		return s.save()
	}
	return nil
}

// GetUnreferencedObjects returns objects with ref_count <= 0
func (s *Storage) GetUnreferencedObjects() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var hashes []string
	for hash, obj := range s.objects {
		if obj.RefCount <= 0 {
			hashes = append(hashes, hash)
		}
	}
	return hashes, nil
}

// StoreSnapshot stores a snapshot
func (s *Storage) StoreSnapshot(hash, treeHash, message, author, email string, timestamp time.Time, parentHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.snapshots[hash] = Snapshot{
		Hash:      hash,
		TreeHash:  treeHash,
		Message:   message,
		Author:    author,
		Email:     email,
		Timestamp: timestamp,
		ParentHash: parentHash,
	}
	return s.save()
}

// GetSnapshot retrieves a snapshot
func (s *Storage) GetSnapshot(hash string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if snap, exists := s.snapshots[hash]; exists {
		return &snap, nil
	}
	return nil, fmt.Errorf("snapshot not found: %s", hash)
}

// GetSnapshots returns all snapshots
func (s *Storage) GetSnapshots(limit int) ([]Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var snaps []Snapshot
	for _, snap := range s.snapshots {
		snaps = append(snaps, snap)
	}
	
	// Sort by timestamp descending
	for i := 0; i < len(snaps)-1; i++ {
		for j := i + 1; j < len(snaps); j++ {
			if snaps[j].Timestamp.After(snaps[i].Timestamp) {
				snaps[i], snaps[j] = snaps[j], snaps[i]
			}
		}
	}
	
	if limit > 0 && len(snaps) > limit {
		snaps = snaps[:limit]
	}
	
	return snaps, nil
}

// SetRef sets a reference
func (s *Storage) SetRef(name, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.refs[name] = Ref{
		Name:      name,
		Hash:      hash,
		UpdatedAt: time.Now(),
	}
	return s.save()
}

// GetRef retrieves a reference
func (s *Storage) GetRef(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if ref, exists := s.refs[name]; exists {
		return ref.Hash, nil
	}
	return "", nil
}

// GetRefs returns all references
func (s *Storage) GetRefs() ([]Ref, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var refs []Ref
	for _, ref := range s.refs {
		refs = append(refs, ref)
	}
	return refs, nil
}

// Close closes the storage
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save()
}

// ObjectsDir returns the objects directory path
func (s *Storage) ObjectsDir() string {
	return s.objectsDir
}
