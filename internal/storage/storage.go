package storage

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Object represents a stored object in the database
type Object struct {
	Hash      string
	Type      string
	Size      int64
	CreatedAt time.Time
	RefCount  int
}

// Snapshot represents a snapshot record
type Snapshot struct {
	Hash      string
	TreeHash  string
	Message   string
	Author    string
	Email     string
	Timestamp time.Time
	ParentHash sql.NullString
}

// Ref represents a reference (branch/tag)
type Ref struct {
	Name      string
	Hash      string
	UpdatedAt time.Time
}

// Storage is the main storage interface
type Storage struct {
	db        *sql.DB
	objectsDir string
	mu        sync.RWMutex
}

// NewStorage creates a new storage instance
func NewStorage(rootPath string) (*Storage, error) {
	pebbleDir := filepath.Join(rootPath, ".pebble")
	objectsDir := filepath.Join(pebbleDir, "objects")
	
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create objects dir: %w", err)
	}
	
	dbPath := filepath.Join(pebbleDir, "pebble.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	
	storage := &Storage{
		db:         db,
		objectsDir: objectsDir,
	}
	
	if err := storage.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}
	
	return storage, nil
}

// initSchema initializes the database schema
func (s *Storage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS objects (
		hash TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		size INTEGER NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		ref_count INTEGER DEFAULT 0
	);
	
	CREATE TABLE IF NOT EXISTS snapshots (
		hash TEXT PRIMARY KEY,
		tree_hash TEXT NOT NULL,
		message TEXT NOT NULL,
		author TEXT NOT NULL,
		email TEXT NOT NULL,
		timestamp TIMESTAMP NOT NULL,
		parent_hash TEXT
	);
	
	CREATE TABLE IF NOT EXISTS refs (
		name TEXT PRIMARY KEY,
		hash TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_objects_type ON objects(type);
	CREATE INDEX IF NOT EXISTS idx_objects_refcount ON objects(ref_count);
	CREATE INDEX IF NOT EXISTS idx_snapshots_timestamp ON snapshots(timestamp);
	CREATE INDEX IF NOT EXISTS idx_refs_updated ON refs(updated_at);
	`
	
	_, err := s.db.Exec(schema)
	return err
}

// StoreObject stores an object
func (s *Storage) StoreObject(hash, objType string, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO objects (hash, type, size, ref_count)
		VALUES (?, ?, ?, 1)
	`, hash, objType, size)
	
	return err
}

// GetObject retrieves an object
func (s *Storage) GetObject(hash string) (*Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var obj Object
	err := s.db.QueryRow(`
		SELECT hash, type, size, created_at, ref_count
		FROM objects WHERE hash = ?
	`, hash).Scan(&obj.Hash, &obj.Type, &obj.Size, &obj.CreatedAt, &obj.RefCount)
	
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("object not found: %s", hash)
	}
	if err != nil {
		return nil, err
	}
	
	return &obj, nil
}

// IncrementRefCount increments the reference count
func (s *Storage) IncrementRefCount(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	_, err := s.db.Exec(`
		UPDATE objects SET ref_count = ref_count + 1 WHERE hash = ?
	`, hash)
	return err
}

// DecrementRefCount decrements the reference count
func (s *Storage) DecrementRefCount(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	_, err := s.db.Exec(`
		UPDATE objects SET ref_count = ref_count - 1 WHERE hash = ?
	`, hash)
	return err
}

// GetUnreferencedObjects returns objects with ref_count = 0
func (s *Storage) GetUnreferencedObjects() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	rows, err := s.db.Query(`SELECT hash FROM objects WHERE ref_count <= 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}
	
	return hashes, nil
}

// StoreSnapshot stores a snapshot
func (s *Storage) StoreSnapshot(hash, treeHash, message, author, email string, timestamp time.Time, parentHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	_, err := s.db.Exec(`
		INSERT INTO snapshots (hash, tree_hash, message, author, email, timestamp, parent_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, hash, treeHash, message, author, email, timestamp, parentHash)
	
	return err
}

// GetSnapshot retrieves a snapshot
func (s *Storage) GetSnapshot(hash string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var snap Snapshot
	err := s.db.QueryRow(`
		SELECT hash, tree_hash, message, author, email, timestamp, parent_hash
		FROM snapshots WHERE hash = ?
	`, hash).Scan(&snap.Hash, &snap.TreeHash, &snap.Message, &snap.Author, 
		&snap.Email, &snap.Timestamp, &snap.ParentHash)
	
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("snapshot not found: %s", hash)
	}
	if err != nil {
		return nil, err
	}
	
	return &snap, nil
}

// GetSnapshots returns all snapshots
func (s *Storage) GetSnapshots(limit int) ([]Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	query := `SELECT hash, tree_hash, message, author, email, timestamp, parent_hash
		FROM snapshots ORDER BY timestamp DESC`
	
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var snapshots []Snapshot
	for rows.Next() {
		var snap Snapshot
		if err := rows.Scan(&snap.Hash, &snap.TreeHash, &snap.Message, 
			&snap.Author, &snap.Email, &snap.Timestamp, &snap.ParentHash); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snap)
	}
	
	return snapshots, nil
}

// SetRef sets a reference
func (s *Storage) SetRef(name, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO refs (name, hash, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, name, hash)
	
	return err
}

// GetRef retrieves a reference
func (s *Storage) GetRef(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var hash string
	err := s.db.QueryRow(`SELECT hash FROM refs WHERE name = ?`, name).Scan(&hash)
	
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	
	return hash, nil
}

// GetRefs returns all references
func (s *Storage) GetRefs() ([]Ref, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	rows, err := s.db.Query(`SELECT name, hash, updated_at FROM refs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var refs []Ref
	for rows.Next() {
		var ref Ref
		if err := rows.Scan(&ref.Name, &ref.Hash, &ref.UpdatedAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	
	return refs, nil
}

// Close closes the storage
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// ObjectHashFromBytes computes the hash of bytes
func ObjectHashFromBytes(data []byte) string {
	// Using SHA-256 (in production, would use a proper hash)
	// For now, we'll use a simple hex encoding
	return hex.EncodeToString(data[:32])
}
