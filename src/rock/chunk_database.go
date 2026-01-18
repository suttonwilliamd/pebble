// Chunk Database
// Purpose: Stores chunk metadata and references.

package rock

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type ChunkMetadata struct {
	Hash string
	Size int
	Data []byte
}

type ChunkDatabase struct {
	DB *sql.DB
}

func NewChunkDatabase(dbPath string) (*ChunkDatabase, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	cd := &ChunkDatabase{DB: db}
	if err := cd.initializeDatabase(); err != nil {
		return nil, err
	}

	return cd, nil
}

func (cd *ChunkDatabase) initializeDatabase() error {
	query := `
	CREATE TABLE IF NOT EXISTS chunks (
		hash TEXT PRIMARY KEY,
		size INTEGER NOT NULL,
		data BLOB
	);`

	_, err := cd.DB.Exec(query)
	return err
}

func (cd *ChunkDatabase) InsertChunk(chunkHash string, size int, data []byte) error {
	query := `INSERT OR IGNORE INTO chunks (hash, size, data) VALUES (?, ?, ?)`
	_, err := cd.DB.Exec(query, chunkHash, size, data)
	return err
}

func (cd *ChunkDatabase) GetChunk(chunkHash string) (*ChunkMetadata, error) {
	query := `SELECT hash, size, data FROM chunks WHERE hash = ?`
	row := cd.DB.QueryRow(query, chunkHash)

	var chunk ChunkMetadata
	err := row.Scan(&chunk.Hash, &chunk.Size, &chunk.Data)
	if err != nil {
		return nil, err
	}

	return &chunk, nil
}