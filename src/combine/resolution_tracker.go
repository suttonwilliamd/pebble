// Resolution Tracker
// Purpose: Tracks the resolution status of conflicts.

package combine

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

type ResolutionStatus struct {
	ConflictID int
	Status     string // "pending", "resolved"
}

type ResolutionTracker struct {
	DB *sql.DB
}

func NewResolutionTracker(dbPath string) (*ResolutionTracker, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	rt := &ResolutionTracker{DB: db}
	if err := rt.initializeDatabase(); err != nil {
		return nil, err
	}

	return rt, nil
}

func (rt *ResolutionTracker) initializeDatabase() error {
	query := `
	CREATE TABLE IF NOT EXISTS resolution_status (
		conflict_id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		status TEXT NOT NULL
	);`

	_, err := rt.DB.Exec(query)
	return err
}

func (rt *ResolutionTracker) TrackResolution(conflictID int, status string) error {
	query := `UPDATE resolution_status SET status = ? WHERE conflict_id = ?`
	_, err := rt.DB.Exec(query, status, conflictID)
	return err
}

func (rt *ResolutionTracker) GetResolutionStatus(conflictID int) (string, error) {
	query := `SELECT status FROM resolution_status WHERE conflict_id = ?`
	var status string
	err := rt.DB.QueryRow(query, conflictID).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}