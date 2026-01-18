// Reference Manager
// Purpose: Manages references to chunks for deduplication and garbage collection.

package rock

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type ReferenceManager struct {
	DB *sql.DB
}

func NewReferenceManager(dbPath string) (*ReferenceManager, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	rm := &ReferenceManager{DB: db}
	if err := rm.initializeDatabase(); err != nil {
		return nil, err
	}

	return rm, nil
}

func (rm *ReferenceManager) initializeDatabase() error {
	query := `
	CREATE TABLE IF NOT EXISTS chunk_references (
		chunk_hash TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 0
	);`

	_, err := rm.DB.Exec(query)
	return err
}

func (rm *ReferenceManager) UpdateReferenceCount(chunkHash string, delta int) error {
	var currentCount int
	query := `SELECT count FROM chunk_references WHERE chunk_hash = ?`
	err := rm.DB.QueryRow(query, chunkHash).Scan(&currentCount)

	if err != nil {
		if err == sql.ErrNoRows {
			// Insert new record
			query = `INSERT INTO chunk_references (chunk_hash, count) VALUES (?, ?)`
			_, err = rm.DB.Exec(query, chunkHash, delta)
			return err
		}
		return err
	}

	newCount := currentCount + delta
	if newCount < 0 {
		newCount = 0
	}

	query = `UPDATE chunk_references SET count = ? WHERE chunk_hash = ?`
	_, err = rm.DB.Exec(query, newCount, chunkHash)
	return err
}

func (rm *ReferenceManager) GetReferenceCount(chunkHash string) (int, error) {
	query := `SELECT count FROM chunk_references WHERE chunk_hash = ?`
	var count int
	err := rm.DB.QueryRow(query, chunkHash).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (rm *ReferenceManager) GetUnreferencedChunks() ([]string, error) {
	query := `SELECT chunk_hash FROM chunk_references WHERE count = 0`
	rows, err := rm.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []string
	for rows.Next() {
		var chunkHash string
		if err := rows.Scan(&chunkHash); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunkHash)
	}

	return chunks, nil
}