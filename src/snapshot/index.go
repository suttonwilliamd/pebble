// Index
// Purpose: Provides fast lookups for objects and snapshots.

package snapshot

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type Index struct {
	DB *sql.DB
}

func NewIndex(dbPath string) (*Index, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	idx := &Index{DB: db}
	if err := idx.initializeDatabase(); err != nil {
		return nil, err
	}

	return idx, nil
}

func (idx *Index) initializeDatabase() error {
	queries := []string{
		`CREATE INDEX IF NOT EXISTS idx_objects_hash ON objects(hash)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_timestamp ON snapshots(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshot_objects_snapshot_id ON snapshot_objects(snapshot_id)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshot_objects_object_hash ON snapshot_objects(object_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshot_objects_path ON snapshot_objects(path)`,
	}

	for _, query := range queries {
		if _, err := idx.DB.Exec(query); err != nil {
			return err
		}
	}

	return nil
}

func (idx *Index) SearchObjects(query string) ([]Object, error) {
	searchQuery := `%` + query + `%`
	q := `SELECT hash, type, size, data FROM objects WHERE hash LIKE ?`
	rows, err := idx.DB.Query(q, searchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var obj Object
		if err := rows.Scan(&obj.Hash, &obj.Type, &obj.Size, &obj.Data); err != nil {
			return nil, err
		}
		objects = append(objects, obj)
	}

	return objects, nil
}

func (idx *Index) SearchSnapshots(query string) ([]Snapshot, error) {
	searchQuery := `%` + query + `%`
	q := `SELECT id, timestamp, root_hash FROM snapshots WHERE timestamp LIKE ?`
	rows, err := idx.DB.Query(q, searchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []Snapshot
	for rows.Next() {
		var snapshot Snapshot
		if err := rows.Scan(&snapshot.ID, &snapshot.Timestamp, &snapshot.RootHash); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}

	return snapshots, nil
}

func (idx *Index) SearchSnapshotObjects(snapshotID int, query string) ([]SnapshotObject, error) {
	searchQuery := `%` + query + `%`
	q := `
	SELECT o.hash, o.type, o.size, o.data, so.path
	FROM objects o
	JOIN snapshot_objects so ON o.hash = so.object_hash
	WHERE so.snapshot_id = ? AND so.path LIKE ?`
	rows, err := idx.DB.Query(q, snapshotID, searchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []SnapshotObject
	for rows.Next() {
		var obj SnapshotObject
		var data []byte
		if err := rows.Scan(&obj.ObjectHash, &obj.Type, &obj.Size, &data, &obj.Path); err != nil {
			return nil, err
		}
		objects = append(objects, obj)
	}

	return objects, nil
}