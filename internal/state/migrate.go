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
//	8 - add `judge_prompt_sections` single-row table. Stores the
//	    user-defined extra prompt sections (title + content pairs) that
//	    are appended to the judge prompt. Persisting them server-side
//	    means the backend's headless auto-approve uses the same rules
//	    as the frontend settings page.
//	9 - add `child_sessions` table. Tracks sessions spawned by the
//	    MCP session-split tools (split_to_session, split_to_worktree).
//	    Keyed by the child session ID; stores the parent session ID,
//	    intent, composed prompt, worktree path, tmux target, status,
//	    and completion summary. Index on (parent_session_id, status)
//	    for the watcher loop and list_child_sessions tool.
//	10 - add `judge_settings` single-row table. Stores the delay (ms)
//	    the backend waits after a permission.asked event before starting
//	    the LLM judge, giving the human a window to respond manually.
//	    Default 5000 ms. Stored server-side so the delay is consistent
//	    across all clients and headless runs.
//	11 - add `reasoning` column to `auto_approved_permission`. Stores
//	    the LLM judge's one-line conclusion so the UI can show *why*
//	    a permission was approved without opening the judge session.
//	    NOT NULL DEFAULT '' so pre-v11 rows show no reasoning rather
//	    than NULL, matching how the judge_session_id column was added.
//	12 - add generic `setting` key/value table. Stores small singleton
//	    settings that don't justify their own dedicated table (e.g. the
//	    PR/Issue prompt templates from the pr-issue-sidebar feature).
//	    Future small settings can reuse this without a new migration.
//	13 - add `share_link` table. Backs the conversation export/share
//	    feature: each row is an unguessable token granting public,
//	    read-only access to a single session's conversation. Keyed by
//	    the token; stores the owning (platform, session_id), creation
//	    time, an optional expiry, and an optional revocation time.
//	14 - multi-remote support. Adds two tables:
//	      * `instance_identity` single-row (id=1) holding this ocman's
//	        stable random instance ID and remote-access token, generated
//	        on first startup. See spec/multi-remote-support AD-5.
//	      * `remote` holding hub-side records of attached remote ocman
//	        instances: a hub-local surrogate PK (local_id), a nullable
//	        learned remote_id (populated after a successful Hello),
//	        address, AES-GCM-encrypted token, enabled flag, health, and
//	        reported hostname/protocol version. See AD-10 / AD-10b.
//	15 - agent loops. Adds two tables and one column:
//	      * `loops` holding one row per self-driving loop (trigger,
//	        action, stop conditions, state, budget counters). See
//	        spec/agent-loops/architecture.md AD-9 / Data Model.
//	      * `loop_iterations` holding the per-loop audit trail (one row
//	        per fired action, idempotency outbox via outcome='pending').
//	      * `child_sessions.loop_id` (nullable) linking a spawned child
//	        to its owning loop; NULL preserves one-shot watcher behavior.
//	16 - agent loops: dedicated session. Adds `loops.loop_session_id`
//	      (nullable) holding the loop's own session (separate from the
//	      creator/owner session), and `loops.session_mode` controlling
//	      whether each iteration spawns a fresh session ('fresh',
//	      default) or reuses the loop session ('reuse'). See
//	      spec/agent-loops Open Question 5.
//	17 - agent loops: model selection. Adds `loops.model`, an optional
//	      platform model reference passed to loop prompts/spawned sessions.
//	18 - agent loops: budget baseline. Adds `loops.usage_baseline_at`.
//	19 - project archive. Adds `archived_project` keyed by the folded
//	      project-root directory. Independent of session archive state so
//	      a project can stay hidden even with no current sessions;
//	      auto-unarchives when a session's activity is newer than
//	      archived_at.
//
// The `schema_version` table tracks applied migrations so each step runs
// exactly once. A fresh database is migrated up to latestSchemaVersion
// in a single pass.
const latestSchemaVersion = 20

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
	case 8:
		return migrateToV8(tx)
	case 9:
		return migrateToV9(tx)
	case 10:
		return migrateToV10(tx)
	case 11:
		return migrateToV11(tx)
	case 12:
		return migrateToV12(tx)
	case 13:
		return migrateToV13(tx)
	case 14:
		return migrateToV14(tx)
	case 15:
		return migrateToV15(tx)
	case 16:
		return migrateToV16(tx)
	case 17:
		return migrateToV17(tx)
	case 18:
		return migrateToV18(tx)
	case 19:
		return migrateToV19(tx)
	case 20:
		return migrateToV20(tx)
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

// migrateToV8 creates the judge_prompt_sections table. It holds a
// single row (id=1) containing the user-defined extra sections
// appended to the LLM judge prompt. Stored as a JSON array of
// {title, content} objects matching the PromptSection type used in
// internal/server/autoapprove.go. An empty JSON array means no extra
// sections.
func migrateToV8(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE judge_prompt_sections (
			id            INTEGER PRIMARY KEY CHECK (id = 1),
			sections_json TEXT    NOT NULL DEFAULT '[]',
			updated_at    INTEGER NOT NULL
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

// migrateToV10 creates the judge_settings table. It holds a single row
// (id=1) with the delay_ms column: how long the backend waits after a
// permission.asked event before starting the LLM judge. Default 5000 ms.
func migrateToV10(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE judge_settings (
			id       INTEGER PRIMARY KEY CHECK (id = 1),
			delay_ms INTEGER NOT NULL DEFAULT 5000
		);
		INSERT INTO judge_settings (id, delay_ms) VALUES (1, 5000);
	`)
	return err
}

// migrateToV11 adds the `reasoning` column to auto_approved_permission.
// The column stores the LLM judge's one-line conclusion ("reasoning"
// field of the JSON it emits) so the UI can show *why* an action was
// approved or flagged without opening the judge session. NOT NULL with
// a ” default keeps pre-v11 rows readable and matches the convention
// used for judge_session_id in v7.
func migrateToV11(tx *sql.Tx) error {
	_, err := tx.Exec(`
		ALTER TABLE auto_approved_permission
		ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''
	`)
	return err
}

// migrateToV9 creates the child_sessions table used by the MCP
// session-split tools. Each row tracks one child session spawned from
// a parent session via split_to_session or split_to_worktree.
//
// The index on (parent_session_id, status) serves two hot paths:
//   - list_child_sessions: WHERE parent_session_id = ?
//   - the watcher loop: WHERE status IN ('starting','running')
func migrateToV9(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE child_sessions (
			id                TEXT    NOT NULL PRIMARY KEY,
			platform          TEXT    NOT NULL,
			parent_session_id TEXT    NOT NULL,
			intent            TEXT    NOT NULL,
			composed_prompt   TEXT    NOT NULL DEFAULT '',
			worktree_path     TEXT,
			branch            TEXT,
			tmux_target       TEXT,
			status            TEXT    NOT NULL DEFAULT 'starting',
			created_at        INTEGER NOT NULL,
			completed_at      INTEGER,
			summary           TEXT
		)`,
		`CREATE INDEX child_sessions_parent_status
			ON child_sessions (parent_session_id, status)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateToV12 creates the generic `setting` key/value table. Used by
// the pr-issue-sidebar feature to persist user-customizable prompt
// templates, and available to any future small singleton setting that
// doesn't justify its own dedicated table.
func migrateToV12(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE setting (
			key        TEXT    PRIMARY KEY,
			value      TEXT    NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	return err
}

// migrateToV13 creates the `share_link` table that backs the
// conversation export/share feature. Each row maps an unguessable
// token to a single session, granting public read-only access.
func migrateToV13(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE share_link (
			token      TEXT    PRIMARY KEY,
			platform   TEXT    NOT NULL,
			session_id TEXT    NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER,
			revoked_at INTEGER
		)`,
		`CREATE INDEX share_link_session
			ON share_link (platform, session_id)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateToV14 creates the multi-remote-support tables.
//
// instance_identity is a single-row (id=1) table holding this ocman's
// own stable random instance ID and remote-access token; both are
// generated on first startup (see internal/state/identity.go) and
// survive restarts (FR-2, FR-4).
//
// remote holds hub-side records of attached remote ocman instances.
// local_id is the hub-local surrogate primary key (so a record can be
// persisted before the remote's real instance ID is learned). remote_id
// is the learned random instance ID, nullable until a successful Hello
// and UNIQUE so the same remote can't be configured twice (AD-10b).
// token_encrypted holds the remote's access token protected with AES-GCM
// using an app-local key (AD-10 / NFR-4).
func migrateToV14(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE instance_identity (
			id           INTEGER PRIMARY KEY CHECK (id = 1),
			instance_id  TEXT    NOT NULL,
			remote_token TEXT    NOT NULL,
			created_at   INTEGER NOT NULL
		)`,
		`CREATE TABLE remote (
			local_id         INTEGER PRIMARY KEY AUTOINCREMENT,
			remote_id        TEXT    UNIQUE,
			display_name     TEXT    NOT NULL DEFAULT '',
			address          TEXT    NOT NULL,
			token_encrypted  BLOB    NOT NULL,
			enabled          INTEGER NOT NULL DEFAULT 1,
			created_at       INTEGER NOT NULL,
			last_seen        INTEGER NOT NULL DEFAULT 0,
			last_health      TEXT    NOT NULL DEFAULT '',
			hostname         TEXT    NOT NULL DEFAULT '',
			protocol_version INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateToV15 creates the agent-loops tables and links child sessions
// to loops. See spec/agent-loops/architecture.md (Migration v15).
//
// `loops` holds one row per self-driving loop. trigger_config and
// stop_conditions are JSON blobs (the engine owns their shape so the
// schema stays stable as new trigger/stop kinds are added). tokens_used
// / cost_usd are cached budget counters refreshed each engine tick.
//
// `loop_iterations` is the audit trail and idempotency outbox: a row is
// created with outcome='pending' before the action's side effect, then
// updated to ok/error afterwards (AD-5a).
//
// child_sessions.loop_id is nullable; NULL keeps the existing one-shot
// report-once behavior for non-loop children (AD-4).
func migrateToV15(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE loops (
			id              TEXT    NOT NULL PRIMARY KEY,
			platform        TEXT    NOT NULL,
			root_session_id TEXT    NOT NULL,
			parent_loop_id  TEXT,
			directory       TEXT    NOT NULL DEFAULT '',
			project_name    TEXT    NOT NULL DEFAULT '',
			title           TEXT    NOT NULL DEFAULT '',
			description     TEXT    NOT NULL DEFAULT '',
			current_task    TEXT    NOT NULL DEFAULT '',
			pattern         TEXT    NOT NULL DEFAULT '',
			trigger_type    TEXT    NOT NULL,
			trigger_config  TEXT    NOT NULL DEFAULT '{}',
			action_type     TEXT    NOT NULL,
			action_template TEXT    NOT NULL DEFAULT '',
			stop_conditions TEXT    NOT NULL DEFAULT '{}',
			state           TEXT    NOT NULL DEFAULT 'active',
			iteration       INTEGER NOT NULL DEFAULT 0,
			error_streak    INTEGER NOT NULL DEFAULT 0,
			tokens_used     INTEGER NOT NULL DEFAULT 0,
			cost_usd        REAL    NOT NULL DEFAULT 0,
			last_fired_at   INTEGER NOT NULL DEFAULT 0,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
			completed_at    INTEGER,
			last_summary    TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX loops_state ON loops (state)`,
		`CREATE INDEX loops_root_session ON loops (root_session_id)`,
		`CREATE INDEX loops_directory ON loops (directory)`,
		`CREATE TABLE loop_iterations (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			loop_id           TEXT    NOT NULL,
			seq               INTEGER NOT NULL,
			fired_at          INTEGER NOT NULL,
			started_at        INTEGER NOT NULL DEFAULT 0,
			completed_at      INTEGER NOT NULL DEFAULT 0,
			trigger_detail    TEXT    NOT NULL DEFAULT '',
			rendered_prompt   TEXT    NOT NULL DEFAULT '',
			target_session_id TEXT    NOT NULL DEFAULT '',
			child_session_id  TEXT    NOT NULL DEFAULT '',
			outcome           TEXT    NOT NULL DEFAULT 'pending',
			summary           TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX loop_iterations_loop_seq ON loop_iterations (loop_id, seq)`,
		`ALTER TABLE child_sessions ADD COLUMN loop_id TEXT`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateToV16 gives loops a dedicated session. loop_session_id holds the
// loop's own session (separate from the creator/owner session in
// root_session_id), populated lazily on first fire. session_mode controls
// per-iteration behavior: 'fresh' (default) spawns a new session each
// iteration; 'reuse' re-prompts the dedicated session. See
// spec/agent-loops Open Question 5.
func migrateToV16(tx *sql.Tx) error {
	stmts := []string{
		`ALTER TABLE loops ADD COLUMN loop_session_id TEXT`,
		`ALTER TABLE loops ADD COLUMN session_mode TEXT NOT NULL DEFAULT 'fresh'`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateToV17 lets loops pin the model used when prompting or spawning.
func migrateToV17(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE loops ADD COLUMN model TEXT NOT NULL DEFAULT ''`)
	return err
}

// migrateToV18 adds loops.usage_baseline_at: a Unix-ms cutoff for budget
// accounting. Only child sessions created at or after this time count
// toward the loop's cost/token budget. It's 0 for existing loops (count
// everything, unchanged behavior) and set to "now" on Restart so a
// restarted loop runs against a fresh budget instead of instantly
// re-tripping on the previous run's spend.
func migrateToV18(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE loops ADD COLUMN usage_baseline_at INTEGER NOT NULL DEFAULT 0`)
	return err
}

// migrateToV19 adds project-level archive state. The key is the folded
// project-root directory (see frontend projectRootForDirectory), so a
// repo and its worktrees archive as one project. archived_at lets the
// server auto-unarchive when newer session activity arrives, mirroring
// the per-session archive semantics.
func migrateToV19(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE archived_project (
		project_root TEXT    NOT NULL PRIMARY KEY,
		archived_at  INTEGER NOT NULL
	)`)
	return err
}

// migrateToV20 lets loops set create-time session settings for the
// sessions they spawn/prompt: the composer agent, the model reasoning
// variant, and a permission ruleset (raw JSON array). All default to
// empty for existing loops (platform defaults, no rules — unchanged
// behavior).
func migrateToV20(tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE loops ADD COLUMN agent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE loops ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE loops ADD COLUMN permission_rules TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
