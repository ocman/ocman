package state

import (
	"context"
	"strconv"
	"time"
)

type InitialArchiveSession struct {
	ID          string
	TimeUpdated int64
}

// ArchiveInitialSessions commits the one-time marker and archive rows together.
// A retry cannot hide a session the user restored after the first cleanup.
// The state database is owner-local; platform completes the host/platform key.
func (d *DB) ArchiveInitialSessions(ctx context.Context, platform string, sessions []InitialArchiveSession) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `INSERT INTO setting (key, value, updated_at)
		VALUES (?, ?, ?) ON CONFLICT(key) DO NOTHING`, initialImportArchiveKey(platform), strconv.FormatInt(now, 10), now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	for _, s := range sessions {
		_, err := tx.ExecContext(ctx, `INSERT INTO archived_session
			(platform, session_id, session_time_updated, archived_at)
			SELECT ?, ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM pinned_session WHERE platform = ? AND session_id = ?)
			AND NOT EXISTS (SELECT 1 FROM unarchived_entity
				WHERE kind = 'session' AND remote_id = 'local' AND entity_key = ?)
			ON CONFLICT(platform, session_id) DO NOTHING`,
			platform, s.ID, s.TimeUpdated, now, platform, s.ID, sessionKey(platform, s.ID))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func initialImportArchiveKey(platform string) string { return "initial_import_archive/" + platform }

// InitialImportArchiveRecent keeps the ordinary inactivity sweep from undoing
// the first-import selection on the same startup. Its normal policy resumes
// after the first day.
func (d *DB) InitialImportArchiveRecent(ctx context.Context, platform string, cutoff int64) (bool, error) {
	value, ok, err := d.GetSetting(ctx, initialImportArchiveKey(platform))
	if err != nil || !ok {
		return false, err
	}
	at, err := strconv.ParseInt(value, 10, 64)
	return at >= cutoff, err
}
