package rock

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// ChunkDatabase manages chunk metadata in SQLite
type ChunkDatabase struct {
	db *sql.DB
	path string
}

// NewChunkDatabase creates a new chunk database
func NewChunkDatabase(dbPath string) (*ChunkDatabase, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	cdb := &ChunkDatabase{
		db:   db,
		path: dbPath,
	}

	if err := cdb.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return cdb, nil
}

// Init initializes the database schema
func (cdb *ChunkDatabase) init() error {
	schema := `
	-- Chunks table: stores chunk content and metadata
	CREATE TABLE IF NOT EXISTS chunks (
		hash TEXT PRIMARY KEY,
		size INTEGER NOT NULL,
		content BLOB,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);

	-- Chunk references table: links chunks to files
	CREATE TABLE IF NOT EXISTS chunk_refs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		chunk_hash TEXT NOT NULL,
		chunk_offset INTEGER NOT NULL,
		chunk_size INTEGER NOT NULL,
		FOREIGN KEY (chunk_hash) REFERENCES chunks(hash)
	);

	-- Index for looking up refs by file
	CREATE INDEX IF NOT EXISTS idx_chunk_refs_file ON chunk_refs(file	-- Index for_path);
	
 looking up refs by hash
	CREATE INDEX IF NOT EXISTS idx_chunk_refs_hash ON chunk_refs(chunk_hash);
	`

	_, err := cdb.db.Exec(schema)
	return err
}

// Close closes the database connection
func (cdb *ChunkDatabase) Close() error {
	return cdb.db.Close()
}

// AddChunk adds a chunk to the database
func (cdb *ChunkDatabase) AddChunk(hash string, size int64, content []byte) error {
	_, err := cdb.db.Exec(
		"INSERT OR IGNORE INTO chunks (hash, size, content) VALUES (?, ?, ?)",
		hash, size, content,
	)
	return err
}

// AddChunkRef adds a reference from a file to a chunk
func (cdb *ChunkDatabase) AddChunkRef(filePath, chunkHash string, offset, size int64) error {
	_, err := cdb.db.Exec(
		"INSERT INTO chunk_refs (file_path, chunk_hash, chunk_offset, chunk_size) VALUES (?, ?, ?, ?)",
		filePath, chunkHash, offset, size,
	)
	return err
}

// GetChunk retrieves a chunk by hash
func (cdb *ChunkDatabase) GetChunk(hash string) (size int64, content []byte, err error) {
	err = cdb.db.QueryRow("SELECT size, content FROM chunks WHERE hash = ?", hash).
		Scan(&size, &content)
	return
}

// GetChunkRefs retrieves all references for a file
func (cdb *ChunkDatabase) GetChunkRefs(filePath string) (refs []ChunkRef, err error) {
	rows, err := cdb.db.Query(
		"SELECT file_path, chunk_hash, chunk_offset, chunk_size FROM chunk_refs WHERE file_path = ? ORDER BY chunk_offset",
		filePath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ref ChunkRef
		if err := rows.Scan(&ref.FilePath, &ref.ChunkHash, &ref.Offset, &ref.Size); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// GetRefCount returns the reference count for a chunk
func (cdb *ChunkDatabase) GetRefCount(hash string) (int, error) {
	var count int
	err := cdb.db.QueryRow(
		"SELECT COUNT(*) FROM chunk_refs WHERE chunk_hash = ?", hash,
	).Scan(&count)
	return count, err
}

// DeleteUnreferencedChunks deletes chunks with no references
func (cdb *ChunkDatabase) DeleteUnreferencedChunks() (int64, error) {
	result, err := cdb.db.Exec(`
		DELETE FROM chunks WHERE hash IN (
			SELECT c.hash FROM chunks c
			LEFT JOIN chunk_refs r ON c.hash = r.chunk_hash
			GROUP BY c.hash
			HAVING COUNT(r.id) = 0
		)
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetStats returns database statistics
func (cdb *ChunkDatabase) GetStats() (totalChunks, totalSize, totalRefs int64, err error) {
	err = cdb.db.QueryRow("SELECT COUNT(*), COALESCE(SUM(size), 0) FROM chunks").
		Scan(&totalChunks, &totalSize)
	if err != nil {
		return
	}

	err = cdb.db.QueryRow("SELECT COUNT(*) FROM chunk_refs").Scan(&totalRefs)
	return
}

// ChunkRef represents a reference from a file to a chunk
type ChunkRef struct {
	FilePath   string
	ChunkHash  string
	Offset     int64
	Size       int64
}

// AddFileChunks adds all chunks for a file with references
func (cdb *ChunkDatabase) AddFileChunks(filePath string, chunks []Chunk) error {
	tx, err := cdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, chunk := range chunks {
		// Add chunk (or ignore if already exists)
		_, err := tx.Exec(
			"INSERT OR IGNORE INTO chunks (hash, size, content) VALUES (?, ?, ?)",
			chunk.Hash, chunk.Size, chunk.Content,
		)
		if err != nil {
			return err
		}

		// Add reference
		_, err = tx.Exec(
			"INSERT INTO chunk_refs (file_path, chunk_hash, chunk_offset, chunk_size) VALUES (?, ?, ?, ?)",
			filePath, chunk.Hash, chunk.Offset, chunk.Size,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetUniqueChunks returns all unique chunks
func (cdb *ChunkDatabase) GetUniqueChunks() (chunks []Chunk, err error) {
	rows, err := cdb.db.Query("SELECT hash, size, content FROM chunks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.Hash, &c.Size, &c.Content); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// Clear removes all data from the database
func (cdb *ChunkDatabase) Clear() error {
	_, err := cdb.db.Exec("DELETE FROM chunk_refs; DELETE FROM chunks;")
	return err
}
