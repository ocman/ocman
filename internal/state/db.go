package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	_ "modernc.org/sqlite"
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
	db, err := otelsql.Open("sqlite", dsn,
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

// OpenFromSQL wraps an existing *sql.DB in a state.DB and runs
// migrations. Intended for tests that open an in-memory database
// directly rather than via Open (which also handles directory creation
// and OTel instrumentation).
func OpenFromSQL(sqlDB *sql.DB) (*DB, error) {
	d := &DB{db: sqlDB}
	if err := d.init(); err != nil {
		return nil, err
	}
	return d, nil
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

// SetAutoApprove enables or disables the auto-approve judge for a
// specific (platform, session) pair. Overwrites any existing row.
func (d *DB) SetAutoApprove(platform, sessionID string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := d.db.Exec(`
		INSERT INTO session_auto_approve (platform, session_id, enabled, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			enabled    = excluded.enabled,
			updated_at = excluded.updated_at
	`, platform, sessionID, val, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("setting auto-approve: %w", err)
	}
	return nil
}

// ApprovedPermission holds the data for one auto-approved permission,
// used to re-inject the notice into the conversation thread on reload.
//
// Reasoning is the LLM judge's one-line conclusion (the "reasoning"
// field of the JSON it emits). Empty when the judge response could
// not be parsed or pre-dates schema v11.
type ApprovedPermission struct {
	PermissionID   string
	PermissionText string
	Patterns       []string
	JudgeSessionID string
	Reasoning      string
	ApprovedAt     int64
}

// RecordApprovedPermission persists one auto-approved permission for a
// session. Idempotent: repeated calls with the same permission_id
// silently overwrite the existing row.
func (d *DB) RecordApprovedPermission(platform, sessionID string, p ApprovedPermission) error {
	patternsJSON, err := encodePatterns(p.Patterns)
	if err != nil {
		return fmt.Errorf("encoding patterns: %w", err)
	}
	_, err = d.db.Exec(`
		INSERT INTO auto_approved_permission
			(platform, session_id, permission_id, permission_text, patterns_json, judge_session_id, reasoning, approved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, session_id, permission_id) DO UPDATE SET
			permission_text  = excluded.permission_text,
			patterns_json    = excluded.patterns_json,
			judge_session_id = excluded.judge_session_id,
			reasoning        = excluded.reasoning,
			approved_at      = excluded.approved_at
	`, platform, sessionID, p.PermissionID, p.PermissionText, patternsJSON, p.JudgeSessionID, p.Reasoning, p.ApprovedAt)
	if err != nil {
		return fmt.Errorf("recording approved permission: %w", err)
	}
	return nil
}

// ListApprovedPermissions returns all auto-approved permissions for a
// session, ordered by approval time ascending.
func (d *DB) ListApprovedPermissions(platform, sessionID string) ([]ApprovedPermission, error) {
	rows, err := d.db.Query(`
		SELECT permission_id, permission_text, patterns_json, judge_session_id, reasoning, approved_at
		FROM auto_approved_permission
		WHERE platform = ? AND session_id = ?
		ORDER BY approved_at ASC
	`, platform, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing approved permissions: %w", err)
	}
	defer rows.Close()

	var out []ApprovedPermission
	for rows.Next() {
		var p ApprovedPermission
		var patternsJSON string
		if err := rows.Scan(&p.PermissionID, &p.PermissionText, &patternsJSON, &p.JudgeSessionID, &p.Reasoning, &p.ApprovedAt); err != nil {
			return nil, fmt.Errorf("scanning approved permission: %w", err)
		}
		p.Patterns, err = decodePatterns(patternsJSON)
		if err != nil {
			p.Patterns = nil
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading approved permissions: %w", err)
	}
	return out, nil
}

// PromptSection is a user-defined extra section appended to the judge prompt.
// Matches the PromptSection type in internal/server/autoapprove.go.
type PromptSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// GetPromptSections returns the persisted judge prompt sections, or an
// empty slice if none have been saved yet.
func (d *DB) GetPromptSections() ([]PromptSection, error) {
	var sectionsJSON string
	err := d.db.QueryRow(`SELECT sections_json FROM judge_prompt_sections WHERE id = 1`).Scan(&sectionsJSON)
	if err == sql.ErrNoRows {
		return []PromptSection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading prompt sections: %w", err)
	}
	var out []PromptSection
	if err := json.Unmarshal([]byte(sectionsJSON), &out); err != nil {
		return []PromptSection{}, nil
	}
	return out, nil
}

// SetPromptSections persists the judge prompt sections, overwriting any
// existing row. Pass an empty slice to clear all custom sections.
func (d *DB) SetPromptSections(sections []PromptSection) error {
	if sections == nil {
		sections = []PromptSection{}
	}
	b, err := json.Marshal(sections)
	if err != nil {
		return fmt.Errorf("encoding prompt sections: %w", err)
	}
	_, err = d.db.Exec(`
		INSERT INTO judge_prompt_sections (id, sections_json, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			sections_json = excluded.sections_json,
			updated_at    = excluded.updated_at
	`, string(b), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("saving prompt sections: %w", err)
	}
	return nil
}

// DefaultJudgeDelayMs is the delay used when no row exists in judge_settings.
const DefaultJudgeDelayMs = 5000

// GetJudgeDelayMs returns the configured delay (ms) the backend waits
// after a permission.asked event before starting the LLM judge.
// Returns defaultJudgeDelayMs when no row has been saved yet.
func (d *DB) GetJudgeDelayMs() (int64, error) {
	var ms int64
	err := d.db.QueryRow(`SELECT delay_ms FROM judge_settings WHERE id = 1`).Scan(&ms)
	if err == sql.ErrNoRows {
		return DefaultJudgeDelayMs, nil
	}
	if err != nil {
		return DefaultJudgeDelayMs, fmt.Errorf("reading judge delay: %w", err)
	}
	return ms, nil
}

// SetJudgeDelayMs persists the judge delay. A value of 0 means no delay
// (judge fires immediately). Negative values are clamped to 0.
func (d *DB) SetJudgeDelayMs(ms int64) error {
	if ms < 0 {
		ms = 0
	}
	_, err := d.db.Exec(`
		INSERT INTO judge_settings (id, delay_ms) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET delay_ms = excluded.delay_ms
	`, ms)
	if err != nil {
		return fmt.Errorf("saving judge delay: %w", err)
	}
	return nil
}

// ChildSession holds the data for one MCP-spawned child session.
type ChildSession struct {
	ID              string
	Platform        string
	ParentSessionID string
	Intent          string
	ComposedPrompt  string
	WorktreePath    string // empty for split_to_session
	Branch          string // empty for split_to_session
	TmuxTarget      string // tmux session or session:window
	Status          string // starting, running, completed, error, cancelled
	CreatedAt       int64
	CompletedAt     int64  // 0 until terminal state
	Summary         string // populated on completion
}

// InsertChildSession persists a new child session record. The initial
// status is always "starting"; callers update it via UpdateChildSession.
func (d *DB) InsertChildSession(cs ChildSession) error {
	_, err := d.db.Exec(`
		INSERT INTO child_sessions
			(id, platform, parent_session_id, intent, composed_prompt,
			 worktree_path, branch, tmux_target, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		cs.ID, cs.Platform, cs.ParentSessionID, cs.Intent, cs.ComposedPrompt,
		nullableString(cs.WorktreePath), nullableString(cs.Branch),
		nullableString(cs.TmuxTarget), cs.Status, cs.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting child session: %w", err)
	}
	return nil
}

// UpdateChildSession updates the mutable fields of a child session
// (status, completed_at, summary). Only the fields that are non-zero
// are updated; callers set CompletedAt and Summary when transitioning
// to a terminal state.
func (d *DB) UpdateChildSession(id, status, summary string, completedAt int64) error {
	_, err := d.db.Exec(`
		UPDATE child_sessions
		SET status       = ?,
		    summary      = CASE WHEN ? != '' THEN ? ELSE summary END,
		    completed_at = CASE WHEN ? != 0  THEN ? ELSE completed_at END
		WHERE id = ?
	`, status, summary, summary, completedAt, completedAt, id)
	if err != nil {
		return fmt.Errorf("updating child session: %w", err)
	}
	return nil
}

// GetChildSession returns a single child session by ID, or an error
// wrapping sql.ErrNoRows when not found.
func (d *DB) GetChildSession(id string) (*ChildSession, error) {
	var cs ChildSession
	var worktreePath, branch, tmuxTarget, summary sql.NullString
	var completedAt sql.NullInt64
	err := d.db.QueryRow(`
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary
		FROM child_sessions WHERE id = ?
	`, id).Scan(
		&cs.ID, &cs.Platform, &cs.ParentSessionID, &cs.Intent, &cs.ComposedPrompt,
		&worktreePath, &branch, &tmuxTarget, &cs.Status,
		&cs.CreatedAt, &completedAt, &summary,
	)
	if err != nil {
		return nil, fmt.Errorf("getting child session: %w", err)
	}
	cs.WorktreePath = worktreePath.String
	cs.Branch = branch.String
	cs.TmuxTarget = tmuxTarget.String
	cs.Summary = summary.String
	if completedAt.Valid {
		cs.CompletedAt = completedAt.Int64
	}
	return &cs, nil
}

// ListChildSessionsByParent returns all child sessions for the given
// parent session ID, ordered by created_at descending (newest first).
func (d *DB) ListChildSessionsByParent(parentSessionID string) ([]ChildSession, error) {
	rows, err := d.db.Query(`
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary
		FROM child_sessions
		WHERE parent_session_id = ?
		ORDER BY created_at DESC
	`, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("listing child sessions: %w", err)
	}
	defer rows.Close()
	return scanChildSessions(rows)
}

// ListNonTerminalChildSessions returns all child sessions whose status
// is "starting" or "running". Used by the watcher loop to find sessions
// that need their completion status checked.
func (d *DB) ListNonTerminalChildSessions() ([]ChildSession, error) {
	rows, err := d.db.Query(`
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary
		FROM child_sessions
		WHERE status IN ('starting', 'running')
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing non-terminal child sessions: %w", err)
	}
	defer rows.Close()
	return scanChildSessions(rows)
}

// CancelChildSession sets the status of a child session to "cancelled"
// and records the completion time. Idempotent: cancelling an already-
// terminal session is a no-op (returns nil).
func (d *DB) CancelChildSession(id string, cancelledAt int64) error {
	_, err := d.db.Exec(`
		UPDATE child_sessions
		SET status       = 'cancelled',
		    completed_at = ?
		WHERE id = ? AND status NOT IN ('completed', 'error', 'cancelled')
	`, cancelledAt, id)
	if err != nil {
		return fmt.Errorf("cancelling child session: %w", err)
	}
	return nil
}

// scanChildSessions scans a *sql.Rows result into a []ChildSession.
func scanChildSessions(rows *sql.Rows) ([]ChildSession, error) {
	var out []ChildSession
	for rows.Next() {
		var cs ChildSession
		var worktreePath, branch, tmuxTarget, summary sql.NullString
		var completedAt sql.NullInt64
		if err := rows.Scan(
			&cs.ID, &cs.Platform, &cs.ParentSessionID, &cs.Intent, &cs.ComposedPrompt,
			&worktreePath, &branch, &tmuxTarget, &cs.Status,
			&cs.CreatedAt, &completedAt, &summary,
		); err != nil {
			return nil, fmt.Errorf("scanning child session: %w", err)
		}
		cs.WorktreePath = worktreePath.String
		cs.Branch = branch.String
		cs.TmuxTarget = tmuxTarget.String
		cs.Summary = summary.String
		if completedAt.Valid {
			cs.CompletedAt = completedAt.Int64
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading child sessions: %w", err)
	}
	return out, nil
}

// nullableString converts an empty string to a nil interface (stored as
// NULL in SQLite) and a non-empty string to itself. This keeps nullable
// TEXT columns clean rather than storing empty strings.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// encodePatterns marshals a string slice to a JSON array string.
func encodePatterns(patterns []string) (string, error) {
	if patterns == nil {
		patterns = []string{}
	}
	b, err := json.Marshal(patterns)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodePatterns unmarshals a JSON array string to a string slice.
func decodePatterns(s string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAutoApprove returns whether the auto-approve judge is explicitly
// enabled for the given session. The second return value is false when
// no per-session override exists (caller should use the global default).
func (d *DB) GetAutoApprove(platform, sessionID string) (enabled bool, exists bool, err error) {
	var val int
	err = d.db.QueryRow(
		`SELECT enabled FROM session_auto_approve WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	).Scan(&val)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("getting auto-approve: %w", err)
	}
	return val != 0, true, nil
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
