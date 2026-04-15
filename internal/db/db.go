package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
}

// DefaultDBPath returns the default path to the OpenCode database.
// Falls back to a relative path if the home directory cannot be determined.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "opencode", "opencode.db")
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// Open opens the OpenCode database in read-only mode.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return &DB{db: db}, nil
}

// OpenReadWrite opens the database in read-write mode. This is intended for
// test setup where schema creation must happen before read-only access.
func OpenReadWrite(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}
