// Reference Counter
// Purpose: Tracks references to objects for garbage collection.

package snapshot

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type ReferenceCounter struct {
	DB *sql.DB
}

func NewReferenceCounter(dbPath string) (*ReferenceCounter, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	rc := &ReferenceCounter{DB: db}
	if err := rc.initializeDatabase(); err != nil {
		return nil, err
	}

	return rc, nil
}

func (rc *ReferenceCounter) initializeDatabase() error {
	query := `
	CREATE TABLE IF NOT EXISTS reference_counts (
		object_hash TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 0
	);`

	_, err := rc.DB.Exec(query)
	return err
}

func (rc *ReferenceCounter) UpdateReferenceCount(objectHash string, delta int) error {
	var currentCount int
	query := `SELECT count FROM reference_counts WHERE object_hash = ?`
	err := rc.DB.QueryRow(query, objectHash).Scan(&currentCount)

	if err != nil {
		if err == sql.ErrNoRows {
			// Insert new record
			query = `INSERT INTO reference_counts (object_hash, count) VALUES (?, ?)`
			_, err = rc.DB.Exec(query, objectHash, delta)
			return err
		}
		return err
	}

	newCount := currentCount + delta
	if newCount < 0 {
		newCount = 0
	}

	query = `UPDATE reference_counts SET count = ? WHERE object_hash = ?`
	_, err = rc.DB.Exec(query, newCount, objectHash)
	return err
}

func (rc *ReferenceCounter) GetReferenceCount(objectHash string) (int, error) {
	query := `SELECT count FROM reference_counts WHERE object_hash = ?`
	var count int
	err := rc.DB.QueryRow(query, objectHash).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (rc *ReferenceCounter) GetUnreferencedObjects() ([]string, error) {
	query := `SELECT object_hash FROM reference_counts WHERE count = 0`
	rows, err := rc.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []string
	for rows.Next() {
		var objectHash string
		if err := rows.Scan(&objectHash); err != nil {
			return nil, err
		}
		objects = append(objects, objectHash)
	}

	return objects, nil
}