// Object Database
// Purpose: Stores snapshot metadata and references to content-addressed objects.

package snapshot

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type Object struct {
	Hash string
	Type string
	Size int64
	Data []byte
}

type Snapshot struct {
	ID        int
	Timestamp int64
	RootHash  string
}

type SnapshotObject struct {
	SnapshotID int
	ObjectHash string
	Path       string
	Type       string
	Size       int64
}

type ObjectDatabase struct {
	DB *sql.DB
}

func NewObjectDatabase(dbPath string) (*ObjectDatabase, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	od := &ObjectDatabase{DB: db}
	if err := od.initializeDatabase(); err != nil {
		return nil, err
	}

	return od, nil
}

func (od *ObjectDatabase) initializeDatabase() error {
	query := `
	CREATE TABLE IF NOT EXISTS objects (
		hash TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		size INTEGER NOT NULL,
		data BLOB
	);

	CREATE TABLE IF NOT EXISTS snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		root_hash TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS snapshot_objects (
		snapshot_id INTEGER NOT NULL,
		object_hash TEXT NOT NULL,
		path TEXT NOT NULL,
		PRIMARY KEY (snapshot_id, object_hash),
		FOREIGN KEY (snapshot_id) REFERENCES snapshots(id),
		FOREIGN KEY (object_hash) REFERENCES objects(hash)
	);`

	_, err := od.DB.Exec(query)
	return err
}

func (od *ObjectDatabase) InsertObject(objectHash string, objectType string, size int64, data []byte) error {
	query := `INSERT OR IGNORE INTO objects (hash, type, size, data) VALUES (?, ?, ?, ?)`
	_, err := od.DB.Exec(query, objectHash, objectType, size, data)
	return err
}

func (od *ObjectDatabase) InsertSnapshot(timestamp int64, rootHash string) (int, error) {
	query := `INSERT INTO snapshots (timestamp, root_hash) VALUES (?, ?)`
	result, err := od.DB.Exec(query, timestamp, rootHash)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	return int(id), err
}

func (od *ObjectDatabase) InsertSnapshotObject(snapshotID int, objectHash string, path string) error {
	query := `INSERT INTO snapshot_objects (snapshot_id, object_hash, path) VALUES (?, ?, ?)`
	_, err := od.DB.Exec(query, snapshotID, objectHash, path)
	return err
}

func (od *ObjectDatabase) GetObject(objectHash string) (*Object, error) {
	query := `SELECT hash, type, size, data FROM objects WHERE hash = ?`
	row := od.DB.QueryRow(query, objectHash)

	var obj Object
	err := row.Scan(&obj.Hash, &obj.Type, &obj.Size, &obj.Data)
	if err != nil {
		return nil, err
	}

	return &obj, nil
}

func (od *ObjectDatabase) GetSnapshotObjects(snapshotID int) ([]SnapshotObject, error) {
	query := `
	SELECT o.hash, o.type, o.size, so.path
	FROM objects o
	JOIN snapshot_objects so ON o.hash = so.object_hash
	WHERE so.snapshot_id = ?`

	rows, err := od.DB.Query(query, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []SnapshotObject
	for rows.Next() {
		var obj SnapshotObject
		if err := rows.Scan(&obj.ObjectHash, &obj.Type, &obj.Size, &obj.Path); err != nil {
			return nil, err
		}
		objects = append(objects, obj)
	}

	return objects, nil
}
