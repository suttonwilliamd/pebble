package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// ObjectType represents the type of a snapshot object
type ObjectType string

const (
	ObjectTypeBlob   ObjectType = "blob"
	ObjectTypeTree  ObjectType = "tree"
	ObjectTypeCommit ObjectType = "commit"
	ObjectTypeIndex ObjectType = "index"
)

// Object represents a content-addressed object
type Object struct {
	Type    ObjectType `json:"type"`
	Size    int64     `json:"size"`
	Hash    string    `json:"hash"`
	Content []byte    `json:"content"`
}

// TreeEntry represents an entry in a tree object
type TreeEntry struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
	Hash string `json:"hash"`
	Type ObjectType `json:"type"`
}

// Tree represents a directory
type Tree struct {
	Entries []TreeEntry `json:"entries"`
	Hash    string     `json:"hash"`
}

// Index represents a file index
type Index struct {
	Version   int       `json:"version"`
	Generator string    `json:"generator"`
	Entries   []IndexEntry `json:"entries"`
	Hash      string    `json:"hash"`
}

// IndexEntry represents an entry in the file index
type IndexEntry struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	Mtime time.Time `json:"mtime"`
	Hash  string `json:"hash"`
}

// Commit represents a commit object
type Commit struct {
	Tree      string            `json:"tree"`
	Parent    string            `json:"parent,omitempty"`
	Author    string            `json:"author"`
	Email     string            `json:"email"`
	Timestamp time.Time         `json:"timestamp"`
	Message   string            `json:"message"`
	Hash      string            `json:"hash"`
}

// Ref represents a branch or tag reference
type Ref struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// NewObject creates a new object with computed hash
func NewObject(objType ObjectType, content []byte) *Object {
	hash := sha256.Sum256(content)
	return &Object{
		Type:    objType,
		Size:    int64(len(content)),
		Hash:    hex.EncodeToString(hash[:]),
		Content: content,
	}
}

// Marshal serializes the object to JSON
func (o *Object) Marshal() ([]byte, error) {
	// Create a serializable version without raw content
	type SerializableObject struct {
		Type ObjectType `json:"type"`
		Size int64      `json:"size"`
		Hash string     `json:"hash"`
	}
	ser := SerializableObject{
		Type: o.Type,
		Size: o.Size,
		Hash: o.Hash,
	}
	return json.Marshal(ser)
}

// ComputeHash computes the SHA-256 hash of content
func ComputeHash(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// CreateBlob creates a blob object from file content
func CreateBlob(path string) (*Object, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return NewObject(ObjectTypeBlob, content), nil
}

// CreateBlobFromReader creates a blob object from a reader
func CreateBlobFromReader(reader io.Reader) (*Object, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	return NewObject(ObjectTypeBlob, content), nil
}

// CreateTree creates a tree object from entries
func CreateTree(entries []TreeEntry) *Object {
	// Sort entries by name for deterministic ordering
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	
	data, _ := json.Marshal(entries)
	return NewObject(ObjectTypeTree, data)
}

// CreateCommit creates a commit object
func CreateCommit(treeHash, parentHash, author, email, message string) *Object {
	commit := Commit{
		Tree:      treeHash,
		Parent:    parentHash,
		Author:    author,
		Email:     email,
		Timestamp: time.Now(),
		Message:   message,
	}
	data, _ := json.Marshal(commit)
	commit.Hash = ComputeHash(data)
	
	// Recompute with hash included
	data, _ = json.Marshal(commit)
	return NewObject(ObjectTypeCommit, data)
}

// CreateIndex creates an index from file entries
func CreateIndex(entries []IndexEntry) *Object {
	// Sort by path for deterministic ordering
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	
	index := Index{
		Version:   2,
		Generator: "pebble/1.0",
		Entries:   entries,
	}
	data, _ := json.Marshal(index)
	index.Hash = ComputeHash(data)
	
	// Recompute with hash
	data, _ = json.Marshal(index)
	return NewObject(ObjectTypeIndex, data)
}
