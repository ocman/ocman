package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// AnalyticsSession and AnalyticsMessage are OpenCode-shaped rows another
// platform contributes to the analytics mirror (AD-12). Data follows the
// mirror's projection: assistant message JSON, or {"role": ...} otherwise.
type AnalyticsSession struct {
	ID, ParentID, Directory, Title string
	TimeCreated                    int64
}

type AnalyticsMessage struct {
	ID, SessionID string
	TimeCreated   int64
	Settled       bool
	Data          string
	// ToolTimes holds one OpenCode-shaped state.time JSON ({"start","end"})
	// per tool call of an assistant message; "" is an unknown timing.
	ToolTimes []string
}

// AnalyticsSource returns every session and the messages created at or
// after since or changed at or after changedSince (0 = all), so the
// existing analytics SQL counts them too. changedSince lets a platform hand
// over old rows it learned about late (a transcript indexed after the last
// sync) without waiting for the next full rebuild.
type AnalyticsSource func(ctx context.Context, since, changedSince int64) ([]AnalyticsSession, []AnalyticsMessage, error)

// noOpenCode identifies a store with no OpenCode database behind it.
const noOpenCode = "platform-sources"

// OpenAnalyticsStore opens analytics-cache.db at path as the only analytics
// database, for an ocman running without OpenCode: platforms fill it through
// AddAnalyticsSource and the existing analytics queries read it. Only the
// analytics methods are meaningful on the returned DB.
func OpenAnalyticsStore(path string) (*DB, error) {
	d := &DB{}
	if err := d.EnableAnalyticsMirror(path, noOpenCode); err != nil {
		return nil, err
	}
	d.db = d.mirror.db
	return d, nil
}

// AddAnalyticsSource adds a platform's rows to the analytics mirror and
// schedules a full rebuild so its history lands at once. Without a mirror
// analytics read OpenCode directly and the source is ignored.
func (d *DB) AddAnalyticsSource(src AnalyticsSource) {
	m := d.mirror
	if m == nil {
		return
	}
	m.sourcesMu.Lock()
	m.sources = append(m.sources, src)
	m.sourcesMu.Unlock()
	m.lastFull.Store(0)
}

// mirrorOnly reports whether the mirror is the only complete analytics
// copy: there is no OpenCode database, or another platform's rows live
// only in the mirror. Then no read may fall back to OpenCode.
func (d *DB) mirrorOnly() bool {
	m := d.mirror
	if m == nil {
		return false
	}
	if d.db == m.db {
		return true
	}
	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()
	return len(m.sources) > 0
}

// completeMirror serves reads when only the mirror holds every platform's
// rows. It never falls back to OpenCode: the first build is awaited, a due
// rebuild runs in the background, and a failed sync reads the copy as it
// stands (possibly stale, but never missing a platform).
func (d *DB) completeMirror(ctx context.Context) *sql.DB {
	m := d.mirror
	var err error
	switch {
	case !m.ready.Load():
		if failed := m.failedAt.Load(); failed == 0 || time.Since(time.UnixMilli(failed)) >= mirrorRetryAfter {
			err = d.syncMirror(ctx, true)
		}
	case m.fullDue():
		d.rebuildMirrorInBackground()
	case !m.fresh():
		err = d.syncMirror(ctx, false)
	}
	if err != nil && !errors.Is(err, errMirrorBusy) {
		log.WithError(err).Warn("syncing analytics mirror; reading the last copy")
	}
	return m.db
}

// sessionsDB is where analytics read the session table: OpenCode itself,
// unless other platforms' sessions exist only in the mirror.
func (d *DB) sessionsDB(ctx context.Context) *sql.DB {
	if d.mirrorOnly() {
		return d.analytics(ctx)
	}
	return d.db
}

// copySources writes the sources' messages (after since, or changed since
// the last sync) and sessions into tx. Called after OpenCode's rows, before
// the orphan cleanup.
func (m *analyticsMirror) copySources(ctx context.Context, tx *sql.Tx, since int64) error {
	m.sourcesMu.Lock()
	sources := append([]AnalyticsSource(nil), m.sources...)
	m.sourcesMu.Unlock()
	changedSince := int64(0)
	if last := m.lastSync.Load(); since > 0 && last > 0 {
		changedSince = time.Unix(0, last).Add(-mirrorSlack).UnixMilli()
	}
	for _, src := range sources {
		// ponytail: a source answers in memory; stream it if a platform's
		// assistant rows ever outgrow that.
		sessions, messages, err := src(ctx, since, changedSince)
		if err != nil {
			return fmt.Errorf("reading analytics source: %w", err)
		}
		for _, msg := range messages {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO message (id, session_id, time_created, data, settled) VALUES (?, ?, ?, ?, ?)`,
				msg.ID, msg.SessionID, msg.TimeCreated, msg.Data, msg.Settled); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM tool_timing WHERE message_id = ?`, msg.ID); err != nil {
				return err
			}
			for _, t := range msg.ToolTimes {
				var timing any
				if t != "" {
					timing = t
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO tool_timing (message_id, time) VALUES (?, ?)`, msg.ID, timing); err != nil {
					return err
				}
			}
		}
		for _, s := range sessions {
			var parent any
			if s.ParentID != "" {
				parent = s.ParentID
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session (id, parent_id, directory, title, time_created) VALUES (?, ?, ?, ?, ?)`,
				s.ID, parent, s.Directory, s.Title, s.TimeCreated); err != nil {
				return err
			}
		}
	}
	return nil
}
