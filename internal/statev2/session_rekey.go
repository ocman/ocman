package statev2

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sessionKeyColumns lists normalized session references, not opaque payloads or
// message/part IDs. Prefixes select the session-scoped checkpoint/receipt keys;
// journal offsets and source-wide verification receipts are unchanged.
var sessionKeyColumns = []struct{ table, column, prefix string }{
	{"session", "id", ""}, {"session", "parent_id", ""},
	{"message", "session_id", ""}, {"part", "session_id", ""},
	{"purged_session", "session_id", ""}, {"change_outbox", "session_id", ""},
	{"analytics_message", "session_id", ""}, {"analytics_part", "session_id", ""},
	{"analytics_session", "id", ""}, {"analytics_session", "parent_id", ""},
	{"analytics_fact", "session_id", ""}, {"analytics_hour", "session_id", ""},
	{"analytics_question", "session_id", ""},
	{"session_sidebar", "id", ""}, {"session_sidebar", "parent_id", ""}, {"sidebar_attention", "session_id", ""},
	{"checkpoint", "stream", "seq/"}, {"checkpoint", "stream", "history/snapshot-v1/"},
	{"source_state", "key", "version/"}, {"source_state", "key", "read-ready/"},
}

// RekeySessions atomically adopts owner-allocated identities for one physical
// history instance. Call only while ingestion and other writers are stopped.
// The caller owns identity allocation and must include references to missing
// parents and purged sessions. No content is reimported or discarded. A SQLite
// backup is created beside the store before the first affected row is changed.
// Repeating a completed mapping is a no-op, without another backup.
func (d *DB) RekeySessions(ctx context.Context, instance string, identities map[string]string) error {
	if instance == "" {
		return fmt.Errorf("statev2: empty rekey instance")
	}
	if _, err := d.Instance(ctx, instance); err != nil {
		return err
	}
	keys := make([]string, 0, len(identities))
	seen := map[string]bool{}
	for old, id := range identities {
		if old == "" || id == "" || seen[id] {
			return fmt.Errorf("statev2: invalid or colliding session rekey: %w", ErrIdentityConflict)
		}
		seen[id] = true
		if old != id {
			keys = append(keys, old)
		}
	}
	for _, old := range keys {
		if next, ok := identities[identities[old]]; ok && next != identities[old] {
			return fmt.Errorf("statev2: chained session rekey: %w", ErrIdentityConflict)
		}
	}
	sort.Strings(keys)
	changed, err := d.hasSessionKeys(ctx, instance, keys)
	if err != nil || !changed {
		return err
	}
	if err := d.backupSessionKeys(ctx); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Existing foreign keys have no ON UPDATE action. Defer their checks until
	// every child key has moved, without disabling foreign-key enforcement.
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		return err
	}
	for _, old := range keys {
		id := identities[old]
		var collision bool
		if err := tx.QueryRowContext(ctx, `SELECT
			(EXISTS(SELECT 1 FROM session WHERE instance_id=? AND id=?) OR EXISTS(SELECT 1 FROM purged_session WHERE instance_id=? AND session_id=?))
			AND (EXISTS(SELECT 1 FROM session WHERE instance_id=? AND id=?) OR EXISTS(SELECT 1 FROM purged_session WHERE instance_id=? AND session_id=?))`,
			instance, old, instance, old, instance, id, instance, id).Scan(&collision); err != nil {
			return err
		}
		if collision {
			return fmt.Errorf("statev2: session rekey would combine retained identities: %w", ErrIdentityConflict)
		}
		for _, ref := range sessionKeyColumns {
			query := "UPDATE " + ref.table + " SET " + ref.column + "=? WHERE instance_id=? AND " + ref.column + "=?"
			if _, err := tx.ExecContext(ctx, query, ref.prefix+id, instance, ref.prefix+old); err != nil {
				return fmt.Errorf("rekeying %s.%s: %w", ref.table, ref.column, err)
			}
		}
		// Normalize's session envelope has one declared parent reference.
		// Provider/tool extensions are not recursively searched or rewritten.
		if _, err := tx.ExecContext(ctx, `UPDATE session SET extensions=json_set(extensions,'$.ParentID',?)
			WHERE instance_id=? AND json_extract(extensions,'$.ParentID')=?`, id, instance, old); err != nil {
			return err
		}
	}
	// Session conflict triggers handle insert/delete, not identity updates.
	// Message IDs did not change, so their conflict markers remain untouched.
	if _, err := tx.ExecContext(ctx, `DELETE FROM analytics_conflict WHERE kind='session';
		INSERT INTO analytics_conflict SELECT 'session',id FROM analytics_session GROUP BY id HAVING count(*)>1;`); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) hasSessionKeys(ctx context.Context, instance string, keys []string) (bool, error) {
	for _, old := range keys {
		for _, ref := range sessionKeyColumns {
			var found bool
			query := "SELECT EXISTS(SELECT 1 FROM " + ref.table + " WHERE instance_id=? AND " + ref.column + "=?)"
			if err := d.db.QueryRowContext(ctx, query, instance, ref.prefix+old).Scan(&found); err != nil {
				return false, err
			}
			if found {
				return true, nil
			}
		}
	}
	return false, nil
}

func (d *DB) backupSessionKeys(ctx context.Context) error {
	file, err := os.CreateTemp(filepath.Dir(d.path), ".statev2-before-session-ids-*.tmp")
	if err != nil {
		return fmt.Errorf("creating session identity backup: %w", err)
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if _, err := d.db.ExecContext(ctx, "VACUUM INTO ?", file.Name()); err != nil {
		return fmt.Errorf("backing up history before session rekey: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), strings.TrimSuffix(file.Name(), ".tmp")+".db"); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(d.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
