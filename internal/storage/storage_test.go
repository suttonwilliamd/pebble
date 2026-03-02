package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewStorage(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	if store == nil {
		t.Fatal("NewStorage returned nil")
	}

	if store.ObjectsDir() == "" {
		t.Error("ObjectsDir is empty")
	}
}

func TestStoreAndGetObject(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	// Store object
	err = store.StoreObject("abc123", "blob", 1024)
	if err != nil {
		t.Fatalf("StoreObject error: %v", err)
	}

	// Get object
	obj, err := store.GetObject("abc123")
	if err != nil {
		t.Fatalf("GetObject error: %v", err)
	}

	if obj.Hash != "abc123" {
		t.Errorf("Hash = %v, want abc123", obj.Hash)
	}
	if obj.Type != "blob" {
		t.Errorf("Type = %v, want blob", obj.Type)
	}
	if obj.Size != 1024 {
		t.Errorf("Size = %v, want 1024", obj.Size)
	}
}

func TestGetObject_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	_, err = store.GetObject("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent object")
	}
}

func TestRefCount(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	// Store object (refcount starts at 1)
	err = store.StoreObject("abc123", "blob", 100)
	if err != nil {
		t.Fatalf("StoreObject error: %v", err)
	}

	obj, _ := store.GetObject("abc123")
	if obj.RefCount != 1 {
		t.Errorf("Initial refcount = %d, want 1", obj.RefCount)
	}

	// Increment
	err = store.IncrementRefCount("abc123")
	if err != nil {
		t.Fatalf("IncrementRefCount error: %v", err)
	}

	obj, _ = store.GetObject("abc123")
	if obj.RefCount != 2 {
		t.Errorf("After increment = %d, want 2", obj.RefCount)
	}

	// Decrement
	err = store.DecrementRefCount("abc123")
	if err != nil {
		t.Fatalf("DecrementRefCount error: %v", err)
	}

	obj, _ = store.GetObject("abc123")
	if obj.RefCount != 1 {
		t.Errorf("After decrement = %d, want 1", obj.RefCount)
	}
}

func TestGetUnreferencedObjects(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	// Store objects
	store.StoreObject("obj1", "blob", 100)
	store.StoreObject("obj2", "blob", 200)
	store.StoreObject("obj3", "blob", 300)

	// Decrement refcount on obj2
	store.DecrementRefCount("obj2")
	store.DecrementRefCount("obj2") // Now 0 or negative

	// Get unreferenced
	unref, err := store.GetUnreferencedObjects()
	if err != nil {
		t.Fatalf("GetUnreferencedObjects error: %v", err)
	}

	// Should have obj2 (refcount <= 0)
	found := false
	for _, hash := range unref {
		if hash == "obj2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected obj2 to be unreferenced")
	}
}

func TestStoreAndGetSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	// Store snapshot
	now := time.Now()
	err = store.StoreSnapshot("snap1", "treehash1", "Test commit", "test@test.com", "Test User", now, "")
	if err != nil {
		t.Fatalf("StoreSnapshot error: %v", err)
	}

	// Get snapshot
	snap, err := store.GetSnapshot("snap1")
	if err != nil {
		t.Fatalf("GetSnapshot error: %v", err)
	}

	if snap.Hash != "snap1" {
		t.Errorf("Hash = %v, want snap1", snap.Hash)
	}
	if snap.TreeHash != "treehash1" {
		t.Errorf("TreeHash = %v, want treehash1", snap.TreeHash)
	}
	if snap.Message != "Test commit" {
		t.Errorf("Message = %v, want 'Test commit'", snap.Message)
	}
}

func TestGetSnapshots(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	// Store multiple snapshots
	now := time.Now()
	store.StoreSnapshot("snap1", "tree1", "First", "a@a.com", "A", now.Add(-2*time.Hour), "")
	store.StoreSnapshot("snap2", "tree2", "Second", "b@b.com", "B", now.Add(-1*time.Hour), "")
	store.StoreSnapshot("snap3", "tree3", "Third", "c@c.com", "C", now, "")

	// Get all
	snaps, err := store.GetSnapshots(0)
	if err != nil {
		t.Fatalf("GetSnapshots error: %v", err)
	}

	if len(snaps) != 3 {
		t.Errorf("Count = %d, want 3", len(snaps))
	}

	// Should be sorted by time descending (newest first)
	if snaps[0].Hash != "snap3" {
		t.Errorf("First should be snap3, got %s", snaps[0].Hash)
	}

	// Test limit
	snaps, err = store.GetSnapshots(2)
	if err != nil {
		t.Fatalf("GetSnapshots error: %v", err)
	}
	if len(snaps) != 2 {
		t.Errorf("Limited count = %d, want 2", len(snaps))
	}
}

func TestRef(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	// Set refs
	err = store.SetRef("main", "abc123")
	if err != nil {
		t.Fatalf("SetRef error: %v", err)
	}
	err = store.SetRef("develop", "def456")
	if err != nil {
		t.Fatalf("SetRef error: %v", err)
	}

	// Get ref
	hash, err := store.GetRef("main")
	if err != nil {
		t.Fatalf("GetRef error: %v", err)
	}
	if hash != "abc123" {
		t.Errorf("main hash = %v, want abc123", hash)
	}

	// Get all refs
	refs, err := store.GetRefs()
	if err != nil {
		t.Fatalf("GetRefs error: %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("Refs count = %d, want 2", len(refs))
	}
}

func TestPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create and store
	store1, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	store1.StoreObject("abc", "blob", 100)
	store1.StoreSnapshot("snap1", "tree1", "msg", "a@a.com", "A", time.Now(), "")
	store1.SetRef("main", "abc")
	store1.Close()

	// Reopen and verify
	store2, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store2.Close()

	obj, err := store2.GetObject("abc")
	if err != nil {
		t.Fatalf("GetObject error: %v", err)
	}
	if obj.Hash != "abc" {
		t.Errorf("Object not persisted")
	}

	snap, err := store2.GetSnapshot("snap1")
	if err != nil {
		t.Fatalf("GetSnapshot error: %v", err)
	}
	if snap.Hash != "snap1" {
		t.Errorf("Snapshot not persisted")
	}

	hash, _ := store2.GetRef("main")
	if hash != "abc" {
		t.Errorf("Ref not persisted")
	}
}

func TestObjectsDir(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage error: %v", err)
	}
	defer store.Close()

	expected := filepath.Join(tmpDir, ".pebble", "objects")
	if store.ObjectsDir() != expected {
		t.Errorf("ObjectsDir = %v, want %v", store.ObjectsDir(), expected)
	}
}
