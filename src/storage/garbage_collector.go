package storage

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// GarbageCollector is responsible for removing unreferenced objects
type GarbageCollector struct {
	cas        *ContentAddressedStorage
	metadataDB *MetadataDatabase
	db         *sql.DB
}

// NewGarbageCollector creates a new GarbageCollector instance
func NewGarbageCollector(cas *ContentAddressedStorage, metadataDB *MetadataDatabase, dbPath string) (*GarbageCollector, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	return &GarbageCollector{
		cas:        cas,
		metadataDB: metadataDB,
		db:         db,
	}, nil
}

// CollectGarbage identifies and removes unreferenced objects
func (gc *GarbageCollector) CollectGarbage() error {
	// Find unreferenced objects
	unreferencedObjects, err := gc.findUnreferencedObjects()
	if err != nil {
		return err
	}

	// Remove unreferenced objects
	for _, hash := range unreferencedObjects {
		err := gc.cas.DeleteObject(hash)
		if err != nil {
			return err
		}

		// Remove metadata
		_, err = gc.db.Exec("DELETE FROM metadata WHERE hash = ?", hash)
		if err != nil {
			return err
		}
	}

	return nil
}

// findUnreferencedObjects finds objects with no references
func (gc *GarbageCollector) findUnreferencedObjects() ([]string, error) {
	rows, err := gc.db.Query(`
		SELECT hash
		FROM metadata
		WHERE id NOT IN (SELECT object_id FROM ref_counts)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var unreferencedObjects []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		unreferencedObjects = append(unreferencedObjects, hash)
	}

	return unreferencedObjects, nil
}

// Close closes the database connection
func (gc *GarbageCollector) Close() error {
	return gc.db.Close()
}
