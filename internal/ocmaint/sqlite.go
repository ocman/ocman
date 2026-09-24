package ocmaint

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// OpenCode stores a full patch per changed file in a user message's
// summary.diffs and repeats it in every message.updated event. Nothing
// but OpenCode's web per-turn changes view reads it, and it dominates the
// database. These statements move it into a separate dump database that
// can be put back.

const dumpSchema = `
CREATE TABLE IF NOT EXISTS dump.message_diffs (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	diffs TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dump.event_diffs (
	id TEXT PRIMARY KEY,
	aggregate_id TEXT NOT NULL,
	diffs TEXT NOT NULL
);`

// oldSessions selects sessions last updated before the cutoff (ms).
const oldSessions = `SELECT id FROM session WHERE time_updated < ?`

// openWritable opens one pinned read-write connection to the OpenCode
// database. ATTACH is per connection, so every step runs on this one.
func openWritable(ctx context.Context, path string) (*sql.DB, *sql.Conn, error) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	// ocman's own read pool keeps querying; wait for it instead of failing.
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout = 60000`); err != nil {
		conn.Close()
		db.Close()
		return nil, nil, err
	}
	return db, conn, nil
}

func attachDump(ctx context.Context, conn *sql.Conn, dumpPath string) error {
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS dump`, dumpPath); err != nil {
		return fmt.Errorf("attaching dump: %w", err)
	}
	if _, err := conn.ExecContext(ctx, dumpSchema); err != nil {
		return fmt.Errorf("creating dump schema: %w", err)
	}
	return nil
}

// backupTo writes a consistent copy of the database to path.
func backupTo(ctx context.Context, conn *sql.Conn, path string) error {
	_, err := conn.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// dumpDiffs copies the diffs of old sessions into the dump. INSERT OR
// IGNORE keeps the first copy when a run is repeated.
func dumpDiffs(ctx context.Context, conn *sql.Conn, cutoffMs int64) (messages, events int64, err error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO dump.message_diffs (id, session_id, diffs)
		SELECT id, session_id, json_extract(data, '$.summary.diffs') FROM message
		WHERE session_id IN (`+oldSessions+`) AND json_type(data, '$.summary.diffs') IS NOT NULL`, cutoffMs)
	if err != nil {
		return 0, 0, fmt.Errorf("dumping message diffs: %w", err)
	}
	messages, _ = res.RowsAffected()
	res, err = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO dump.event_diffs (id, aggregate_id, diffs)
		SELECT id, aggregate_id, json_extract(data, '$.info.summary.diffs') FROM event
		WHERE type = 'message.updated.1' AND aggregate_id IN (`+oldSessions+`)
		  AND json_type(data, '$.info.summary.diffs') IS NOT NULL`, cutoffMs)
	if err != nil {
		return 0, 0, fmt.Errorf("dumping event diffs: %w", err)
	}
	events, _ = res.RowsAffected()
	return messages, events, tx.Commit()
}

// stripDiffs removes diffs, but only from rows whose diffs are in the
// dump, so nothing is removed that can't be restored.
func stripDiffs(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE message SET data = json_remove(data, '$.summary.diffs')
		WHERE id IN (SELECT id FROM dump.message_diffs)
		  AND json_type(data, '$.summary.diffs') IS NOT NULL`); err != nil {
		return fmt.Errorf("stripping message diffs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE event SET data = json_remove(data, '$.info.summary.diffs')
		WHERE id IN (SELECT id FROM dump.event_diffs)
		  AND json_type(data, '$.info.summary.diffs') IS NOT NULL`); err != nil {
		return fmt.Errorf("stripping event diffs: %w", err)
	}
	return tx.Commit()
}

// restoreDiffs puts dumped diffs back on rows that no longer have them.
func restoreDiffs(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE message SET data = json_set(data, '$.summary.diffs', json(d.diffs))
		FROM dump.message_diffs AS d
		WHERE message.id = d.id AND json_type(message.data, '$.summary') = 'object'
		  AND json_type(message.data, '$.summary.diffs') IS NULL`); err != nil {
		return fmt.Errorf("restoring message diffs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE event SET data = json_set(event.data, '$.info.summary.diffs', json(d.diffs))
		FROM dump.event_diffs AS d
		WHERE event.id = d.id AND json_type(event.data, '$.info.summary') = 'object'
		  AND json_type(event.data, '$.info.summary.diffs') IS NULL`); err != nil {
		return fmt.Errorf("restoring event diffs: %w", err)
	}
	return tx.Commit()
}

// compact rewrites the database without the freed pages, then truncates
// the WAL the rewrite went through.
func compact(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return checkpoint(ctx, conn)
}

func checkpoint(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	return nil
}
