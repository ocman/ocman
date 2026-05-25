package state

import (
	"database/sql"
	"fmt"
	"time"
)

// Schema versions:
//
//	1 - initial schema (archived_session, seen_session keyed by session_id).
//	2 - multi-platform: add `platform TEXT NOT NULL DEFAULT 'opencode'`
//	    column + recreate tables with (platform, session_id) composite
//	    primary key. See spec/multi-agent-support/architecture.md AD-10.
//	3 - add `auth_secret` single-row table holding the HMAC key used to
//	    sign auth cookies. Persisting the key across restarts keeps
//	    logged-in clients logged in up to the cookie TTL.
//	4 - add `model_favorite` table keyed by (platform, provider_id,
//	    model_id). Scoped per-platform so OpenCode's "claude-opus-4"
//	    and Claude Code's same model are tracked independently, mirroring
//	    how archived_session / seen_session are scoped.
//	5 - add `pinned_session` table keyed by (platform, session_id).
//	    Lets the user pin sessions to the top of the sidebar. The
//	    pinned_at timestamp determines sort order within the pinned
//	    group (most recently pinned first).
//	6 - add `session_auto_approve` table keyed by (platform, session_id).
//	    Records sessions where the user has enabled the auto-approve
//	    judge. Absent row = not enabled (or inherits the server default).
//	7 - add `auto_approved_permission` table. Each row records one
//	    permission that was auto-approved by the LLM judge, so the
//	    approval notice can be re-injected into the conversation thread
//	    after a page refresh.
//
// The `schema_version` table tracks applied migrations so each step runs
// exactly once. A fresh database is migrated up to latestSchemaVersion
// in a single pass.
const latestSchemaVersion = 7

// migrate brings the state database up to latestSchemaVersion. Safe to
// call on every startup: idempotent, no-op once already current.
//
// Migrations run inside a single transaction so a crash mid-migration
// can never leave the database in a half-converted state. SQLite lets
// us wrap DDL in a transaction; the whole operation either commits or
// rolls back.
func migrate(db *sql.DB) error {
	if err := ensureSchemaVersionTable(db); err != nil {
		return err
	}
	current, err := currentSchemaVersion(db)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	defer func() {
		// Roll back on any path that doesn't reach Commit. Ignored if
		// the transaction has already committed.
		_ = tx.Rollback()
	}()

	for v := current + 1; v <= latestSchemaVersion; v++ {
		if err := applyMigration(tx, v); err != nil {
			return fmt.Errorf("migrate to v%d: %w", v, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
			v, time.Now().UnixMilli(),
		); err != nil {
			return fmt.Errorf("recording schema_version v%d: %w", v, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}
	return nil
}

// ensureSchemaVersionTable creates the schema_version table if absent.
// This is outside the migration transaction because a fresh database
// needs it before we can even read "what version are we at?".
func ensureSchemaVersionTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("creating schema_version: %w", err)
	}
	return nil
}

// currentSchemaVersion returns the highest applied version, or 0 if
// the table is empty. A fresh database starts at 0 and migrates up.
//
// Tables created by a previous ocman version (before schema_version
// existed) are detected by checking for the `archived_session` table
// and treating its presence as "at v1".
func currentSchemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow(`SELECT COALESCE(max(version), 0) FROM schema_version`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("reading schema_version: %w", err)
	}
	if version > 0 {
		return version, nil
	}

	// Fallback: schema_version empty but archived_session already
	// exists from a pre-schema_version ocman build. Treat as v1.
	var exists int
	err = db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='archived_session'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("probing for legacy archived_session table: %w", err)
	}
	if exists > 0 {
		return 1, nil
	}
	return 0, nil
}

// applyMigration runs the DDL for the given target version.
func applyMigration(tx *sql.Tx, target int) error {
	switch target {
	case 1:
		return migrateToV1(tx)
	case 2:
		return migrateToV2(tx)
	case 3:
		return migrateToV3(tx)
	case 4:
		return migrateToV4(tx)
	case 5:
		return migrateToV5(tx)
	case 6:
		return migrateToV6(tx)
	case 7:
		return migrateToV7(tx)
	default:
		return fmt.Errorf("no migration registered for v%d", target)
	}
}

// migrateToV1 creates the original single-platform tables. Only runs
// on a fresh database; existing ocman installs jump straight to v1's
// shape via the bootstrapped CREATE TABLE IF NOT EXISTS in Open.
func migrateToV1(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS archived_session (
			session_id TEXT PRIMARY KEY,
			session_time_updated INTEGER NOT NULL,
			archived_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS seen_session (
			session_id TEXT PRIMARY KEY,
			session_time_updated INTEGER NOT NULL,
			seen_at INTEGER NOT NULL
		);
	`)
	return err
}

// migrateToV2 widens the primary key on both tables from (session_id)
// to (platform, session_id). SQLite can't ALTER a primary key in
// place, so each table is rebuilt: create a new table with the
// target shape, copy rows (defaulting platform='opencode' since that
// was ocman's only platform before v2), drop the old, rename.
func migrateToV2(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE archived_session_v2 (
			platform             TEXT    NOT NULL,
			session_id           TEXT    NOT NULL,
			session_time_updated INTEGER NOT NULL,
			archived_at          INTEGER NOT NULL,
			PRIMARY KEY (platform, session_id)
		)`,
		`INSERT INTO archived_session_v2 (platform, session_id, session_time_updated, archived_at)
		 SELECT 'opencode', session_id, session_time_updated, archived_at
		 FROM archived_session`,
		`DROP TABLE archived_session`,
		`ALTER TABLE archived_session_v2 RENAME TO archived_session`,

		`CREATE TABLE seen_session_v2 (
			platform             TEXT    NOT NULL,
			session_id           TEXT    NOT NULL,
			session_time_updated INTEGER NOT NULL,
			seen_at              INTEGER NOT NULL,
			PRIMARY KEY (platform, session_id)
		)`,
		`INSERT INTO seen_session_v2 (platform, session_id, session_time_updated, seen_at)
		 SELECT 'opencode', session_id, session_time_updated, seen_at
		 FROM seen_session`,
		`DROP TABLE seen_session`,
		`ALTER TABLE seen_session_v2 RENAME TO seen_session`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateToV3 creates the auth_secret table. The table is constrained
// to a single row (id=1) so AuthSecret() / SetAuthSecret() can always
// upsert without juggling row identity.
func migrateToV3(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE auth_secret (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			hmac_key   BLOB    NOT NULL,
			created_at INTEGER NOT NULL
		)
	`)
	return err
}

// migrateToV4 creates the model_favorite table. The primary key is
// (platform, provider_id, model_id) so the same provider/model pair
// can be favorited independently across platforms — matches the
// scoping of archived_session / seen_session.
//
// provider_id may be empty for platforms that don't have a provider
// concept (Claude Code treats the model id as the whole thing), but
// is still part of the PK so "anthropic/claude-opus-4" doesn't collide
// with a bare "claude-opus-4".
func migrateToV4(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE model_favorite (
			platform    TEXT    NOT NULL,
			provider_id TEXT    NOT NULL,
			model_id    TEXT    NOT NULL,
			created_at  INTEGER NOT NULL,
			PRIMARY KEY (platform, provider_id, model_id)
		)
	`)
	return err
}

// migrateToV5 creates the pinned_session table. The primary key is
// (platform, session_id), matching the scoping of archived_session /
// seen_session. pinned_at stores the Unix-millisecond timestamp of
// when the user pinned the session, used for sort order within the
// pinned group.
func migrateToV5(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE pinned_session (
			platform   TEXT    NOT NULL,
			session_id TEXT    NOT NULL,
			pinned_at  INTEGER NOT NULL,
			PRIMARY KEY (platform, session_id)
		)
	`)
	return err
}

// migrateToV7 creates the auto_approved_permission table. Each row
// records one permission auto-approved by the LLM judge so the notice
// can survive a page refresh. permission_id is the OpenCode-assigned
// request ID; patterns_json stores the patterns array as a JSON string.
func migrateToV7(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE auto_approved_permission (
			platform         TEXT    NOT NULL,
			session_id       TEXT    NOT NULL,
			permission_id    TEXT    NOT NULL,
			permission_text  TEXT    NOT NULL,
			patterns_json    TEXT    NOT NULL DEFAULT '[]',
			judge_session_id TEXT    NOT NULL DEFAULT '',
			approved_at      INTEGER NOT NULL,
			PRIMARY KEY (platform, session_id, permission_id)
		)
	`)
	return err
}

// migrateToV6 creates the session_auto_approve table. Presence of a
// row means auto-approve is explicitly enabled for that session. Absence
// means the server's global default applies. The enabled column is kept
// for forward compatibility (future: allow explicit per-session disable
// even when the global default is on).
func migrateToV6(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE session_auto_approve (
			platform   TEXT    NOT NULL,
			session_id TEXT    NOT NULL,
			enabled    INTEGER NOT NULL DEFAULT 1,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (platform, session_id)
		)
	`)
	return err
}
