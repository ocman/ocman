package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/NoUseFreak/ocman/internal/telemetry"
	log "github.com/sirupsen/logrus"
)

// The analytics mirror is a disposable SQLite copy of the message and session
// columns the analytics queries read. OpenCode stores user-message attachments
// inline in message.data (2 GB of a 2.1 GB table in the wild), so any query
// that has to inspect role reads all of it. The mirror keeps assistant data
// minus fields analytics never reads, and replaces every other row with
// {"role": ...}, so the same SQL runs against a ~10x smaller table. Tables and index keep OpenCode's names,
// which is what lets the analytics queries run unchanged on either handle.
//
// It is derived data: delete the file and it rebuilds. A schema bump or a
// different source database wipes it the same way.
const mirrorSchemaVersion = 3

const mirrorSchema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS session (
	id TEXT PRIMARY KEY,
	parent_id TEXT,
	directory TEXT NOT NULL,
	title TEXT NOT NULL,
	time_created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS message (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	time_created INTEGER NOT NULL,
	settled INTEGER NOT NULL,
	data TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS message_session_time_created_id_idx ON message (session_id, time_created, id);
CREATE INDEX IF NOT EXISTS message_settled_idx ON message (settled, time_created);
CREATE TABLE IF NOT EXISTS tool_timing (message_id TEXT NOT NULL, time TEXT, tool TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS tool_timing_message_idx ON tool_timing (message_id);
`

const (
	// mirrorMaxStaleness bounds how old the mirror may be when a query reads
	// it; older triggers an incremental sync first.
	mirrorMaxStaleness = 5 * time.Second
	// mirrorFullRebuildEvery re-copies everything, catching what the
	// incremental window cannot see: deleted old messages and rows inserted
	// with old timestamps (imports).
	mirrorFullRebuildEvery = 6 * time.Hour
	// mirrorSlack re-reads a margin before the newest copied message, for
	// rows committed slightly out of time_created order.
	mirrorSlack = 10 * time.Minute
	// mirrorUnsettledMaxAge stops re-reading a turn that never finished
	// (crashed or interrupted); the full rebuild still refreshes it. It
	// must stay short: an interrupted turn at the cap holds the whole
	// incremental window (including the tool-timings copy) open that long.
	mirrorUnsettledMaxAge = 1 * time.Hour
	// mirrorRetryAfter spaces out full-build attempts after one fails, so a
	// persistent failure (full disk, unwritable file) does not re-copy the
	// database on every read.
	mirrorRetryAfter = 5 * time.Minute
)

type analyticsMirror struct {
	db *sql.DB
	// gate serialises syncs (capacity 1). A channel rather than a mutex so
	// waiting honours the caller's context.
	gate     chan struct{}
	ready    atomic.Bool
	building atomic.Bool
	lastSync atomic.Int64 // unix nanos of the last successful sync
	// lastFull is the unix millis of the last full rebuild, persisted in
	// meta.full_at so a restart cannot postpone reconciliation.
	lastFull atomic.Int64
	// failedAt is the unix millis of the last failed full build, 0 after a
	// success. While set, an overdue mirror is not trusted.
	failedAt atomic.Int64
}

// errMirrorBusy reports that another sync holds the gate.
var errMirrorBusy = errors.New("analytics mirror sync in progress")

func (m *analyticsMirror) fresh() bool {
	return time.Since(time.Unix(0, m.lastSync.Load())) <= mirrorMaxStaleness
}

func (m *analyticsMirror) fullDue() bool {
	return time.Since(time.UnixMilli(m.lastFull.Load())) > mirrorFullRebuildEvery
}

// EnableAnalyticsMirror opens (or creates) the mirror at path. source
// identifies the OpenCode database; a mirror built from another source is
// wiped. Until the first build completes, analytics read OpenCode directly.
func (d *DB) EnableAnalyticsMirror(path, source string) error {
	mdb, err := telemetry.OpenSQL("file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", "analytics-cache")
	if err != nil {
		return fmt.Errorf("opening analytics mirror: %w", err)
	}
	if err := initMirror(mdb, source); err != nil {
		mdb.Close()
		return err
	}
	m := &analyticsMirror{db: mdb, gate: make(chan struct{}, 1)}
	var fullAt string
	_ = mdb.QueryRow(`SELECT value FROM meta WHERE key = 'full_at'`).Scan(&fullAt)
	if ms, err := strconv.ParseInt(fullAt, 10, 64); err == nil && ms > 0 {
		m.lastFull.Store(ms)
		m.ready.Store(true)
	}
	d.mirror = m
	return nil
}

func initMirror(mdb *sql.DB, source string) error {
	var version int
	if err := mdb.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading analytics mirror version: %w", err)
	}
	var have string
	if version == mirrorSchemaVersion {
		_ = mdb.QueryRow(`SELECT value FROM meta WHERE key = 'source'`).Scan(&have)
	}
	if version != mirrorSchemaVersion || have != source {
		if _, err := mdb.Exec(`DROP TABLE IF EXISTS meta; DROP TABLE IF EXISTS session; DROP TABLE IF EXISTS message; DROP TABLE IF EXISTS tool_timing;`); err != nil {
			return fmt.Errorf("resetting analytics mirror: %w", err)
		}
	}
	if _, err := mdb.Exec(mirrorSchema); err != nil {
		return fmt.Errorf("creating analytics mirror schema: %w", err)
	}
	_, err := mdb.Exec(fmt.Sprintf(`PRAGMA user_version = %d; INSERT OR REPLACE INTO meta (key, value) VALUES ('source', ?)`, mirrorSchemaVersion), source)
	return err
}

// analytics returns the handle analytics queries should read: the mirror
// when built, else OpenCode itself. A stale mirror gets a quick incremental
// sync first. A read never waits on another caller's sync: while one runs,
// it reads the mirror as it stands. Full builds and the periodic rebuild
// (~14s on a large database) run in the background.
func (d *DB) analytics(ctx context.Context) *sql.DB {
	m := d.mirror
	switch {
	case m == nil:
		return d.db
	case !m.ready.Load():
		d.rebuildMirrorInBackground()
		return d.db
	case m.fullDue():
		d.rebuildMirrorInBackground()
		if m.failedAt.Load() != 0 {
			// Rebuilds are failing, so the copy may stay stale for good:
			// cost speed, not correctness, until one succeeds.
			return d.db
		}
		return m.db
	case m.fresh():
		return m.db
	}
	switch err := d.syncMirror(ctx, false); {
	case err == nil, errors.Is(err, errMirrorBusy):
		return m.db
	default:
		if ctx.Err() != nil {
			// The reader's own context died mid-sync. OpenCode cannot be
			// read with it either, so serve the last consistent (stale)
			// mirror instead of selecting a source fallback that cannot succeed.
			return m.db
		}
		log.WithError(err).Warn("syncing analytics mirror; reading OpenCode directly")
		return d.db
	}
}

func (d *DB) rebuildMirrorInBackground() {
	m := d.mirror
	if failed := m.failedAt.Load(); failed != 0 && time.Since(time.UnixMilli(failed)) < mirrorRetryAfter {
		return
	}
	if !m.building.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.building.Store(false)
		if err := d.SyncAnalyticsMirror(context.Background()); err != nil {
			log.WithError(err).Warn("building analytics mirror")
		}
	}()
}

// SyncAnalyticsMirror brings the mirror up to date with OpenCode, waiting
// (until ctx ends) for any sync already running.
func (d *DB) SyncAnalyticsMirror(ctx context.Context) error {
	return d.syncMirror(ctx, true)
}

func (d *DB) syncMirror(ctx context.Context, wait bool) error {
	m := d.mirror
	if wait {
		select {
		case m.gate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else {
		select {
		case m.gate <- struct{}{}:
		default:
			return errMirrorBusy
		}
	}
	defer func() { <-m.gate }()
	full := !m.ready.Load() || m.fullDue()
	if !full && m.fresh() {
		return nil // another caller just synced
	}
	since := int64(0)
	if !full {
		var err error
		if since, err = m.windowStart(ctx); err != nil {
			return err
		}
	}
	start := time.Now()
	if err := d.copyToMirror(ctx, since, start); err != nil {
		if full && ctx.Err() == nil {
			m.failedAt.Store(time.Now().UnixMilli())
		}
		return err
	}
	if full {
		m.failedAt.Store(0)
		m.lastFull.Store(start.UnixMilli())
		m.ready.Store(true)
		log.WithField("took", time.Since(start)).Info("analytics mirror rebuilt")
	}
	m.lastSync.Store(start.UnixNano())
	return nil
}

// windowStart is the oldest time_created the incremental sync must re-read:
// the oldest recent unfinished turn, or the newest copied message, less slack.
func (m *analyticsMirror) windowStart(ctx context.Context) (int64, error) {
	var newest, unsettled sql.NullInt64
	cutoff := time.Now().Add(-mirrorUnsettledMaxAge).UnixMilli()
	err := m.db.QueryRowContext(ctx, `SELECT
		(SELECT max(time_created) FROM message),
		(SELECT min(time_created) FROM message WHERE settled = 0 AND time_created > ?)`, cutoff).Scan(&newest, &unsettled)
	if err != nil {
		return 0, err
	}
	since := newest.Int64
	if unsettled.Valid && unsettled.Int64 < since {
		since = unsettled.Int64
	}
	since -= mirrorSlack.Milliseconds()
	if since < 1 {
		since = 1 // never 0: that means "all time" to messagesFrom
	}
	return since, nil
}

// copyToMirror reconciles messages created at or after since and all sessions
// in one transaction, writing only changed rows on incremental syncs.
// Zero since rebuilds everything using a streaming copy, recorded at start.
// Messages go before sessions: a session created in between is still copied, and one
// deleted in between takes its messages with it via the orphan cleanup.
func (d *DB) copyToMirror(ctx context.Context, since int64, start time.Time) error {
	tx, err := d.mirror.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if since == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM message; DELETE FROM tool_timing; DELETE FROM session`); err != nil {
			return err
		}
	}
	q := `SELECT m.id, m.session_id, m.time_created,
			CASE WHEN json_extract(m.data, '$.role') = 'assistant'
				THEN json_remove(m.data, '$.path', '$.parentID') -- ~40% of the row, unused here
				ELSE json_object('role', json_extract(m.data, '$.role')) END,
			json_extract(m.data, '$.role') != 'assistant'
				OR json_extract(m.data, '$.time.completed') IS NOT NULL
				OR json_extract(m.data, '$.finish') IS NOT NULL
				OR json_extract(m.data, '$.error') IS NOT NULL
		FROM ` + messagesFrom(since, false)
	var args []any
	if since > 0 {
		q += ` WHERE m.time_created >= ?`
		args = append(args, since)
	}
	messageInsert := `INSERT OR REPLACE INTO message (id, session_id, time_created, data, settled) VALUES (?, ?, ?, ?, ?)`
	removed := since == 0
	if since == 0 {
		err = copyRows(ctx, d.db, tx, q, args, messageInsert, 5)
	} else {
		removed, err = reconcileMirrorRows(ctx, d.db, tx, q, args,
			`SELECT id, session_id, time_created, data, settled FROM message WHERE time_created >= ?`,
			messageInsert, `DELETE FROM message WHERE id = ?`, 5)
	}
	if err != nil {
		return fmt.Errorf("copying messages to analytics mirror: %w", err)
	}
	if err := d.copyToolTimings(ctx, tx, since); err != nil {
		return fmt.Errorf("copying tool timings to analytics mirror: %w", err)
	}

	sessionQuery := `SELECT id, parent_id, directory, title, time_created FROM session`
	sessionInsert := `INSERT INTO session (id, parent_id, directory, title, time_created) VALUES (?, ?, ?, ?, ?)`
	if since == 0 {
		err = copyRows(ctx, d.db, tx, sessionQuery, nil, sessionInsert, 5)
	} else {
		var sessionsRemoved bool
		sessionsRemoved, err = reconcileMirrorRows(ctx, d.db, tx, sessionQuery, nil,
			sessionQuery, sessionInsert, `DELETE FROM session WHERE id = ?`, 5)
		removed = removed || sessionsRemoved
	}
	if err != nil {
		return fmt.Errorf("copying sessions to analytics mirror: %w", err)
	}
	// Source reads are separate snapshots: a newly copied session can disappear
	// before the session scan, even when no previously mirrored session was removed.
	result, err := tx.ExecContext(ctx, `DELETE FROM message WHERE session_id NOT IN (SELECT id FROM session)`)
	if err != nil {
		return err
	}
	orphans, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed || orphans > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tool_timing WHERE message_id NOT IN (SELECT id FROM message)`); err != nil {
			return err
		}
	}
	if since == 0 {
		// Committed with the copy itself: full_at never claims a rebuild
		// that did not land.
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES ('full_at', ?)`,
			strconv.FormatInt(start.UnixMilli(), 10)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// copyRows streams the n-column result of query on src into insert on tx.
func copyRows(ctx context.Context, src *sql.DB, tx *sql.Tx, query string, args []any, insert string, n int) error {
	rows, err := src.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	ins, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		return err
	}
	defer ins.Close()
	vals := make([]any, n)
	ptrs := make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		if _, err := ins.ExecContext(ctx, vals...); err != nil {
			return err
		}
	}
	return rows.Err()
}
