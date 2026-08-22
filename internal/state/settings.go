package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// GetSetting returns the string value stored under key. The second
// return value is false when no row exists for the key, distinguishing
// "explicit empty string" from "not set". Callers typically fall back
// to a default when ok is false.
func (d *DB) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = d.db.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("getting setting %q: %w", key, err)
	}
	return value, true, nil
}

// settingWorktreeInheritPermissions is the KV key controlling whether a
// worktree/child session inherits the parent's accumulated always-allow
// permissions at split time (issue #101). Values "1" / "0"; default on.
const settingWorktreeInheritPermissions = "worktree.inherit_permissions"

// GetWorktreeInheritPermissions reports whether worktree sessions
// inherit the parent's approved permissions. Defaults to true when the
// setting has never been written.
func (d *DB) GetWorktreeInheritPermissions(ctx context.Context) (bool, error) {
	val, ok, err := d.GetSetting(ctx, settingWorktreeInheritPermissions)
	if err != nil {
		return true, err
	}
	if !ok {
		return true, nil
	}
	return val != "0", nil
}

// SetWorktreeInheritPermissions persists the worktree-inherit-permissions
// toggle as "1" / "0".
func (d *DB) SetWorktreeInheritPermissions(ctx context.Context, enabled bool) error {
	val := "0"
	if enabled {
		val = "1"
	}
	return d.SetSetting(ctx, settingWorktreeInheritPermissions, val)
}

// SetSetting inserts or updates the value for the given key. Empty
// string values are valid and persist (they are distinguishable from
// "not set" via GetSetting's ok return).
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx, `
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
