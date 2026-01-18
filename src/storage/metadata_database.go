package storage

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// MetadataDatabase is responsible for storing metadata for objects and snapshots
type MetadataDatabase struct {
	db *sql.DB
}

// NewMetadataDatabase creates a new MetadataDatabase instance
func NewMetadataDatabase(dbPath string) (*MetadataDatabase, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create tables if they don't exist
	err = createMetadataTables(db)
	if err != nil {
		return nil, err
	}

	return &MetadataDatabase{db: db}, nil
}

// createMetadataTables creates the necessary tables for the metadata database
func createMetadataTables(db *sql.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS metadata (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hash TEXT UNIQUE NOT NULL,
			type TEXT NOT NULL,
			size INTEGER NOT NULL,
			timestamp INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS ref_counts (
			object_id INTEGER PRIMARY KEY,
			count INTEGER NOT NULL,
			FOREIGN KEY (object_id) REFERENCES metadata(id)
		)`,
	}

	for _, table := range tables {
		_, err := db.Exec(table)
		if err != nil {
			return err
		}
	}

	return nil
}

// StoreMetadata stores metadata for an object
func (mdb *MetadataDatabase) StoreMetadata(hash string, objectType string, size int64) error {
	_, err := mdb.db.Exec(
		"INSERT OR IGNORE INTO metadata (hash, type, size, timestamp) VALUES (?, ?, ?, datetime('now'))",
		hash, objectType, size,
	)
	return err
}

// GetMetadata retrieves metadata for an object
func (mdb *MetadataDatabase) GetMetadata(hash string) (map[string]interface{}, error) {
	var id int64
	var objectType string
	var size int64
	var timestamp string

	err := mdb.db.QueryRow(
		"SELECT id, type, size, timestamp FROM metadata WHERE hash = ?",
		hash,
	).Scan(&id, &objectType, &size, &timestamp)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":        id,
		"type":      objectType,
		"size":      size,
		"timestamp": timestamp,
	}, nil
}

// IncrementRefCount increments the reference count for an object
func (mdb *MetadataDatabase) IncrementRefCount(objectID int64) error {
	_, err := mdb.db.Exec(
		"INSERT INTO ref_counts (object_id, count) VALUES (?, 1) ON CONFLICT(object_id) DO UPDATE SET count = count + 1",
		objectID,
	)
	return err
}

// DecrementRefCount decrements the reference count for an object
func (mdb *MetadataDatabase) DecrementRefCount(objectID int64) error {
	_, err := mdb.db.Exec(
		"UPDATE ref_counts SET count = count - 1 WHERE object_id = ?",
		objectID,
	)
	return err
}

// GetRefCount retrieves the reference count for an object
func (mdb *MetadataDatabase) GetRefCount(objectID int64) (int, error) {
	var count int
	err := mdb.db.QueryRow("SELECT count FROM ref_counts WHERE object_id = ?", objectID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Close closes the database connection
func (mdb *MetadataDatabase) Close() error {
	return mdb.db.Close()
}
