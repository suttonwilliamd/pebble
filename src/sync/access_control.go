package sync

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// AccessControl is responsible for handling basic access control
type AccessControl struct {
	db *sql.DB
}

// NewAccessControl creates a new AccessControl instance
func NewAccessControl(dbPath string) (*AccessControl, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create tables if they don't exist
	err = createAccessControlTables(db)
	if err != nil {
		return nil, err
	}

	return &AccessControl{db: db}, nil
}

// createAccessControlTables creates the necessary tables for access control
func createAccessControlTables(db *sql.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password TEXT NOT NULL,
			role TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS permissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			role TEXT NOT NULL,
			resource TEXT NOT NULL,
			action TEXT NOT NULL,
			UNIQUE(role, resource, action)
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

// AuthenticateUser authenticates a user
func (ac *AccessControl) AuthenticateUser(username, password string) (bool, error) {
	var storedPassword string
	err := ac.db.QueryRow("SELECT password FROM users WHERE username = ?", username).Scan(&storedPassword)
	if err != nil {
		return false, err
	}

	// TODO: Implement password hashing and comparison
	return storedPassword == password, nil
}

// AuthorizeUser authorizes a user to perform an action on a resource
func (ac *AccessControl) AuthorizeUser(username, resource, action string) (bool, error) {
	var role string
	err := ac.db.QueryRow("SELECT role FROM users WHERE username = ?", username).Scan(&role)
	if err != nil {
		return false, err
	}

	var count int
	err = ac.db.QueryRow("SELECT COUNT(*) FROM permissions WHERE role = ? AND resource = ? AND action = ?", role, resource, action).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// AddUser adds a new user
func (ac *AccessControl) AddUser(username, password, role string) error {
	_, err := ac.db.Exec("INSERT INTO users (username, password, role) VALUES (?, ?, ?)", username, password, role)
	return err
}

// AddPermission adds a new permission
func (ac *AccessControl) AddPermission(role, resource, action string) error {
	_, err := ac.db.Exec("INSERT INTO permissions (role, resource, action) VALUES (?, ?, ?)", role, resource, action)
	return err
}

// Close closes the database connection
func (ac *AccessControl) Close() error {
	return ac.db.Close()
}
