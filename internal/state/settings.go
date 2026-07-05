package state

import (
	"database/sql"
	"fmt"
	"time"
)

// GetSetting returns the string value stored under key. The second
// return value is false when no row exists for the key, distinguishing
// "explicit empty string" from "not set". Callers typically fall back
// to a default when ok is false.
func (d *DB) GetSetting(key string) (value string, ok bool, err error) {
	err = d.db.QueryRow(`SELECT value FROM setting WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("getting setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting inserts or updates the value for the given key. Empty
// string values are valid and persist (they are distinguishable from
// "not set" via GetSetting's ok return).
func (d *DB) SetSetting(key, value string) error {
	_, err := d.db.Exec(`
		INSERT INTO setting (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value      = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	return nil
}
