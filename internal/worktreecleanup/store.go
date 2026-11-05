package worktreecleanup

import (
	"context"
	"database/sql"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/state"
)

var Schema = state.ModuleSchema{
	Module: "worktreecleanup",
	Tables: []string{"worktree_cleanup_activity", "worktree_cleanup_checkout"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE worktree_cleanup_activity (
			platform TEXT NOT NULL, session_id TEXT NOT NULL,
			activity_at INTEGER NOT NULL DEFAULT 0, observed_updated INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(platform, session_id));
			CREATE TABLE worktree_cleanup_checkout (
			owner TEXT NOT NULL, path TEXT NOT NULL, project TEXT NOT NULL,
			branch TEXT NOT NULL, head TEXT NOT NULL, removed_at INTEGER NOT NULL DEFAULT 0,
			last_attempt_at INTEGER NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(owner, path));
			CREATE TRIGGER worktree_cleanup_seen_insert AFTER INSERT ON seen_session BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(NEW.platform,NEW.session_id,NEW.seen_at)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;
			CREATE TRIGGER worktree_cleanup_seen_update AFTER UPDATE ON seen_session BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(NEW.platform,NEW.session_id,NEW.seen_at)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;
			CREATE TRIGGER worktree_cleanup_pin AFTER INSERT ON pinned_session BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(NEW.platform,NEW.session_id,NEW.pinned_at)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;
			CREATE TRIGGER worktree_cleanup_unpin AFTER DELETE ON pinned_session BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(OLD.platform,OLD.session_id,CAST(strftime('%s','now') AS INTEGER)*1000)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;
			CREATE TRIGGER worktree_cleanup_queue_insert AFTER INSERT ON queued_message BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(NEW.platform,NEW.session_id,NEW.created_at)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;
			CREATE TRIGGER worktree_cleanup_queue_delete AFTER DELETE ON queued_message BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(OLD.platform,OLD.session_id,CAST(strftime('%s','now') AS INTEGER)*1000)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;
			CREATE TRIGGER worktree_cleanup_queue_move AFTER UPDATE OF position ON queued_message BEGIN
				INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at)
				VALUES(NEW.platform,NEW.session_id,CAST(strftime('%s','now') AS INTEGER)*1000)
				ON CONFLICT(platform,session_id) DO UPDATE SET activity_at=excluded.activity_at;
			END;`)
		if err != nil {
			return err
		}
		// Existing archives get a full grace period on upgrade, never a purge.
		_, err = tx.Exec(`INSERT INTO worktree_cleanup_activity
			(platform, session_id, activity_at, observed_updated)
			SELECT platform, session_id, ?, session_time_updated FROM archived_session`, time.Now().UnixMilli())
		return err
	}},
}

func (m *Module) touch(ctx context.Context, key state.Key) error {
	_, err := m.d.Store.SQL().ExecContext(ctx, `INSERT INTO worktree_cleanup_activity
		(platform, session_id, activity_at) VALUES (?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET activity_at = excluded.activity_at`, key.Platform, key.SessionID, m.now().UnixMilli())
	return err
}

// Observe uses the local observation time, not another machine's clock.
// Re-reading the same source timestamp does not move the cleanup deadline.
func (m *Module) observe(ctx context.Context, key state.Key, updated, archiveUpdated int64) (int64, error) {
	_, err := m.d.Store.SQL().ExecContext(ctx, `INSERT INTO worktree_cleanup_activity
		(platform, session_id, activity_at, observed_updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
		activity_at = CASE WHEN observed_updated != 0 AND observed_updated != excluded.observed_updated THEN ? ELSE activity_at END,
		observed_updated = excluded.observed_updated`, key.Platform, key.SessionID,
		initialActivity(updated, archiveUpdated, m.now().UnixMilli()), updated, m.now().UnixMilli())
	if err != nil {
		return 0, err
	}
	var at int64
	err = m.d.Store.SQL().QueryRowContext(ctx, `SELECT activity_at FROM worktree_cleanup_activity WHERE platform = ? AND session_id = ?`, key.Platform, key.SessionID).Scan(&at)
	return at, err
}

func initialActivity(updated, archived, now int64) int64 {
	if updated > archived {
		return now
	}
	return 0
}

func (m *Module) remember(ctx context.Context, owner string, checkpoint hostsvc.WorktreeCheckpoint) error {
	_, err := m.d.Store.SQL().ExecContext(ctx, `INSERT INTO worktree_cleanup_checkout
		(owner, path, project, branch, head) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(owner, path) DO UPDATE SET project = excluded.project, branch = excluded.branch,
		head = excluded.head WHERE worktree_cleanup_checkout.removed_at = 0`, owner, checkpoint.Path, checkpoint.Project, checkpoint.Branch, checkpoint.Head)
	return err
}

func (m *Module) outcome(ctx context.Context, owner, path, reason string, removed bool) error {
	at := int64(0)
	if removed {
		at = m.now().UnixMilli()
	}
	_, err := m.d.Store.SQL().ExecContext(ctx, `UPDATE worktree_cleanup_checkout
		SET last_attempt_at = ?, reason = ?, removed_at = MAX(removed_at, ?) WHERE owner = ? AND path = ?`,
		m.now().UnixMilli(), reason, at, owner, path)
	return err
}

func (m *Module) checkpoint(ctx context.Context, owner, path string) (*hostsvc.WorktreeCheckpoint, error) {
	var out hostsvc.WorktreeCheckpoint
	err := m.d.Store.SQL().QueryRowContext(ctx, `SELECT project, path, branch, head
		FROM worktree_cleanup_checkout WHERE owner = ? AND (path = ? OR substr(?,1,length(path)+1) = path||'/')
		ORDER BY length(path) DESC LIMIT 1`, owner, path, path).Scan(&out.Project, &out.Path, &out.Branch, &out.Head)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &out, err
}

func (m *Module) forget(ctx context.Context, owner, path string) error {
	_, err := m.d.Store.SQL().ExecContext(ctx, `DELETE FROM worktree_cleanup_checkout WHERE owner = ? AND path = ?`, owner, path)
	return err
}
