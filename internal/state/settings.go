package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetSetting returns the string value stored under key. The second
// return value is false when no row exists for the key, distinguishing
// "explicit empty string" from "not set". Callers typically fall back
// to a default when ok is false.
func (d *DB) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	if value, ok, managed := d.managedSettingValue(key); managed {
		return value, ok, nil
	}
	if f := d.fileConfig.Load(); f != nil && isFileSetting(key) {
		return f.get(ctx, key)
	}
	err = d.reader().QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&value)
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
// On a remote-managed instance, a setting the hub supplies is read-only.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	if err := d.rejectManagedWrite(key); err != nil {
		return err
	}
	var err error
	if d.fileConfig.Load() != nil && isFileSetting(key) {
		err = d.writeFileSetting(ctx, key, value)
	} else {
		err = d.setSetting(ctx, key, value)
	}
	if err != nil {
		return err
	}
	if key == JudgeModelSettingKey {
		d.notifyManagedSettingsChange()
	}
	return nil
}

func (d *DB) setSetting(ctx context.Context, key, value string) error {
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

// SetSettings saves one settings form atomically. Runtime rows cannot be mixed
// with file-backed settings, since SQLite and the filesystem share no transaction.
func (d *DB) SetSettings(ctx context.Context, values map[string]string) error {
	for key := range values {
		if err := d.rejectManagedWrite(key); err != nil {
			return err
		}
		if d.fileConfig.Load() != nil && !isFileSetting(key) {
			return errors.New("cannot mix runtime state with file-backed settings")
		}
	}
	if d.fileConfig.Load() != nil {
		if err := d.writeFileSettings(ctx, values); err != nil {
			return err
		}
	} else {
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		for key, value := range values {
			if _, err := tx.ExecContext(ctx, `INSERT INTO setting(key,value,updated_at) VALUES(?,?,?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if _, changed := values[JudgeModelSettingKey]; changed {
		d.notifyManagedSettingsChange()
	}
	return nil
}
