package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// DB wraps the writable ocman state database.
type DB struct {
	db *sql.DB
}

// DefaultDBPath returns the default path to the ocman state database.
// Falls back to a relative path if the home directory cannot be determined.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "ocman", "state.db")
	}
	return filepath.Join(home, ".local", "share", "ocman", "state.db")
}

// Open opens the ocman state database and ensures the schema exists.
// Runs any pending migrations exactly once; safe to call on every boot.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", path)
	// See internal/db.Open for the otelsql rationale; same trade-off
	// applies. db.name="ocman" distinguishes ocman's own state from
	// the upstream OpenCode database in trace and metric attributes.
	db, err := otelsql.Open("sqlite3", dsn,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "ocman"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("opening state database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging state database: %w", err)
	}

	stateDB := &DB{db: db}
	if err := stateDB.init(); err != nil {
		db.Close()
		return nil, err
	}
	_, _ = otelsql.RegisterDBStatsMetrics(db,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "ocman"),
		),
	)

	return stateDB, nil
}

// init bootstraps the schema to latestSchemaVersion. See migrate.go
// for the migration plan.
func (d *DB) init() error {
	return migrate(d.db)
}

// Key identifies a session by its owning platform + session ID. Used
// as the map key for state lookups so two different platforms can
// share a session-id namespace without colliding.
type Key struct {
	Platform  string
	SessionID string
}

// MarkSessionSeen records the latest session update the user has
// viewed for the given platform/session. Per-platform: Claude Code's
// session "abc123" and OpenCode's session "abc123" are independent.
func (d *DB) MarkSessionSeen(platform, sessionID string, sessionTimeUpdated int64) error {
	_, err := d.db.Exec(`
		INSERT INTO seen_session (platform, session_id, session_time_updated, seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			session_time_updated = CASE
				WHEN excluded.session_time_updated > seen_session.session_time_updated THEN excluded.session_time_updated
				ELSE seen_session.session_time_updated
			END,
			seen_at = excluded.seen_at
	`, platform, sessionID, sessionTimeUpdated, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("marking session seen: %w", err)
	}
	return nil
}

// SeenSessions returns every seen session's time_updated, keyed by
// (platform, session-id). Callers doing a per-platform lookup can
// construct a Key directly.
func (d *DB) SeenSessions() (map[Key]int64, error) {
	rows, err := d.db.Query(`SELECT platform, session_id, session_time_updated FROM seen_session`)
	if err != nil {
		return nil, fmt.Errorf("listing seen sessions: %w", err)
	}
	defer rows.Close()

	seen := make(map[Key]int64)
	for rows.Next() {
		var platform, sessionID string
		var sessionTimeUpdated int64
		if err := rows.Scan(&platform, &sessionID, &sessionTimeUpdated); err != nil {
			return nil, fmt.Errorf("scanning seen session: %w", err)
		}
		seen[Key{Platform: platform, SessionID: sessionID}] = sessionTimeUpdated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading seen sessions: %w", err)
	}

	return seen, nil
}

// Close closes the state database.
func (d *DB) Close() error {
	return d.db.Close()
}

// ArchiveSession records a session as archived at its current update
// timestamp for the given platform.
func (d *DB) ArchiveSession(platform, sessionID string, sessionTimeUpdated int64) error {
	_, err := d.db.Exec(`
		INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			session_time_updated = excluded.session_time_updated,
			archived_at = excluded.archived_at
	`, platform, sessionID, sessionTimeUpdated, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("archiving session: %w", err)
	}
	return nil
}

// UnarchiveSession removes a session's archived marker (per platform).
func (d *DB) UnarchiveSession(platform, sessionID string) error {
	_, err := d.db.Exec(
		`DELETE FROM archived_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	)
	if err != nil {
		return fmt.Errorf("unarchiving session: %w", err)
	}
	return nil
}

// AuthSecret returns the persisted HMAC key used to sign auth
// cookies, or nil if none has been stored yet. The same key is
// reused across restarts so logged-in clients stay logged in up
// to the cookie TTL.
func (d *DB) AuthSecret() ([]byte, error) {
	var key []byte
	err := d.db.QueryRow(`SELECT hmac_key FROM auth_secret WHERE id = 1`).Scan(&key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading auth_secret: %w", err)
	}
	return key, nil
}

// SetAuthSecret overwrites the persisted HMAC key. Existing cookies
// signed with the previous key become invalid, which is the intended
// behaviour of a rotation.
func (d *DB) SetAuthSecret(key []byte) error {
	_, err := d.db.Exec(`
		INSERT INTO auth_secret (id, hmac_key, created_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			hmac_key = excluded.hmac_key,
			created_at = excluded.created_at
	`, key, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("writing auth_secret: %w", err)
	}
	return nil
}

// ModelFavorite identifies one favorited model. `Provider` may be
// empty for platforms that don't have a provider concept.
type ModelFavorite struct {
	Platform string
	Provider string
	Model    string
}

// AddModelFavorite marks a (platform, provider, model) triple as a
// favorite. Idempotent: repeated calls are no-ops.
func (d *DB) AddModelFavorite(platform, provider, model string) error {
	_, err := d.db.Exec(`
		INSERT INTO model_favorite (platform, provider_id, model_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, provider_id, model_id) DO NOTHING
	`, platform, provider, model, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("adding model favorite: %w", err)
	}
	return nil
}

// RemoveModelFavorite unfavorites a (platform, provider, model)
// triple. No error if the row doesn't exist.
func (d *DB) RemoveModelFavorite(platform, provider, model string) error {
	_, err := d.db.Exec(
		`DELETE FROM model_favorite WHERE platform = ? AND provider_id = ? AND model_id = ?`,
		platform, provider, model,
	)
	if err != nil {
		return fmt.Errorf("removing model favorite: %w", err)
	}
	return nil
}

// ModelFavorites returns every favorited model for the given platform,
// ordered by creation time ascending (oldest favorite first).
func (d *DB) ModelFavorites(platform string) ([]ModelFavorite, error) {
	rows, err := d.db.Query(
		`SELECT platform, provider_id, model_id FROM model_favorite
		 WHERE platform = ?
		 ORDER BY created_at ASC`,
		platform,
	)
	if err != nil {
		return nil, fmt.Errorf("listing model favorites: %w", err)
	}
	defer rows.Close()

	var out []ModelFavorite
	for rows.Next() {
		var f ModelFavorite
		if err := rows.Scan(&f.Platform, &f.Provider, &f.Model); err != nil {
			return nil, fmt.Errorf("scanning model favorite: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading model favorites: %w", err)
	}
	return out, nil
}

// PinSession marks a session as pinned. Idempotent: repeated calls
// are no-ops (pinned_at is not updated).
func (d *DB) PinSession(platform, sessionID string) error {
	_, err := d.db.Exec(`
		INSERT INTO pinned_session (platform, session_id, pinned_at)
		VALUES (?, ?, ?)
		ON CONFLICT(platform, session_id) DO NOTHING
	`, platform, sessionID, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("pinning session: %w", err)
	}
	return nil
}

// UnpinSession removes a session's pinned marker.
func (d *DB) UnpinSession(platform, sessionID string) error {
	_, err := d.db.Exec(
		`DELETE FROM pinned_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	)
	if err != nil {
		return fmt.Errorf("unpinning session: %w", err)
	}
	return nil
}

// PinnedSessions returns every pinned session's pinned_at timestamp,
// keyed by (platform, session-id).
func (d *DB) PinnedSessions() (map[Key]int64, error) {
	rows, err := d.db.Query(`SELECT platform, session_id, pinned_at FROM pinned_session`)
	if err != nil {
		return nil, fmt.Errorf("listing pinned sessions: %w", err)
	}
	defer rows.Close()

	pinned := make(map[Key]int64)
	for rows.Next() {
		var platform, sessionID string
		var pinnedAt int64
		if err := rows.Scan(&platform, &sessionID, &pinnedAt); err != nil {
			return nil, fmt.Errorf("scanning pinned session: %w", err)
		}
		pinned[Key{Platform: platform, SessionID: sessionID}] = pinnedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading pinned sessions: %w", err)
	}

	return pinned, nil
}

// ArchivedSessions returns every archived session's time_updated,
// keyed by (platform, session-id).
func (d *DB) ArchivedSessions() (map[Key]int64, error) {
	rows, err := d.db.Query(`SELECT platform, session_id, session_time_updated FROM archived_session`)
	if err != nil {
		return nil, fmt.Errorf("listing archived sessions: %w", err)
	}
	defer rows.Close()

	archived := make(map[Key]int64)
	for rows.Next() {
		var platform, sessionID string
		var sessionTimeUpdated int64
		if err := rows.Scan(&platform, &sessionID, &sessionTimeUpdated); err != nil {
			return nil, fmt.Errorf("scanning archived session: %w", err)
		}
		archived[Key{Platform: platform, SessionID: sessionID}] = sessionTimeUpdated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading archived sessions: %w", err)
	}

	return archived, nil
}
