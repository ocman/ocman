package statev2

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/rawhistory"
)

const rawReplaySchema = `CREATE TABLE IF NOT EXISTS raw_replay (
	instance_id TEXT NOT NULL, session_id TEXT NOT NULL,
	format TEXT NOT NULL, updated_at INTEGER NOT NULL, gzip BLOB NOT NULL,
	PRIMARY KEY(instance_id, session_id),
	FOREIGN KEY(instance_id, session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS raw_replay_expiry ON raw_replay(updated_at);`

type rawReplayWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (d *DB) saveRawReplay(ctx context.Context, q rawReplayWriter, key Key, archive *rawhistory.Snapshot) error {
	if archive == nil || archive.UpdatedAt <= rawHistoryCutoff(time.Now()) {
		return nil
	}
	_, err := q.ExecContext(ctx, `INSERT INTO raw_replay(instance_id,session_id,format,updated_at,gzip) VALUES(?,?,?,?,?)
		ON CONFLICT(instance_id,session_id) DO UPDATE SET format=excluded.format,updated_at=excluded.updated_at,gzip=excluded.gzip
		WHERE excluded.updated_at>=raw_replay.updated_at AND (excluded.updated_at<>raw_replay.updated_at OR excluded.gzip<>raw_replay.gzip)`,
		key.InstanceID, key.SessionID, archive.Format, archive.UpdatedAt, archive.GZIP)
	return err
}

// SaveRawReplay attaches replay inputs on a verified but unchanged snapshot.
func (d *DB) SaveRawReplay(ctx context.Context, key Key, archive *rawhistory.Snapshot) error {
	return d.saveRawReplay(ctx, d.db, key, archive)
}

// ReadRawReplay returns private replay input; it is not a conversation read.
func (d *DB) ReadRawReplay(ctx context.Context, key Key) (rawhistory.Snapshot, error) {
	var out rawhistory.Snapshot
	err := d.reader().QueryRowContext(ctx, `SELECT format,updated_at,gzip FROM raw_replay WHERE instance_id=? AND session_id=?`, key.InstanceID, key.SessionID).
		Scan(&out.Format, &out.UpdatedAt, &out.GZIP)
	return out, err
}

func sweepRawArchives(ctx context.Context, tx *sql.Tx, cutoff int64) (RawHistorySweep, error) {
	var result RawHistorySweep
	// A compressed snapshot can be large; never delete more than one per batch.
	var row int64
	err := tx.QueryRowContext(ctx, `SELECT rowid,length(gzip) FROM raw_replay
		WHERE updated_at<=? ORDER BY updated_at LIMIT 1`, cutoff).Scan(&row, &result.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM raw_replay WHERE rowid=?`, row); err != nil {
		return result, err
	}
	result.Snapshots = 1
	return result, nil
}
