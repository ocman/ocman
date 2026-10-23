// Package sidebar reads compact session browsing data. It never loads message
// payloads, accounting summaries or platform state during a request.
package sidebar

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/telemetry"
	"github.com/google/uuid"
	"modernc.org/sqlite"
)

var attached sync.Map

func init() {
	sqlite.RegisterConnectionHook(func(c sqlite.ExecQuerierContext, dsn string) error {
		path, ok := attached.Load(dsn)
		if !ok {
			return nil
		}
		_, err := c.ExecContext(context.Background(), `ATTACH DATABASE ? AS core`, []driver.NamedValue{{Ordinal: 1, Value: path.(string)}})
		return err
	})
}

type Scope struct{ Owner, Platform, Source, Generation string }
type Reader struct {
	db  *sql.DB
	dsn string
}

// Open attaches both files read-only. Catalogue publication and purge checks
// remain authoritative rather than an eventually consistent duplicate.
func Open(history, core string) (*Reader, error) {
	if history == "" || core == "" {
		return nil, fmt.Errorf("sidebar: explicit database paths required")
	}
	history, err := filepath.Abs(history)
	if err != nil {
		return nil, err
	}
	core, err = filepath.Abs(core)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: history}
	coreURL := url.URL{Scheme: "file", Path: core, RawQuery: "mode=ro"}
	u.RawQuery = url.Values{"mode": {"ro"}, "sidebar_core": {core}, "sidebar_reader": {uuid.NewString()}, "_pragma": {"busy_timeout(1000)", "query_only(1)"}}.Encode()
	dsn := u.String()
	attached.Store(dsn, coreURL.String())
	conn, err := telemetry.OpenSQL(dsn, "sidebar")
	if err != nil {
		attached.Delete(dsn)
		return nil, err
	}
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(4)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		attached.Delete(dsn)
		return nil, err
	}
	return &Reader{db: conn, dsn: dsn}, nil
}

func (r *Reader) Close() error { err := r.db.Close(); attached.Delete(r.dsn); return err }

func (r *Reader) Sessions(ctx context.Context, scope Scope, q platforms.SidebarQuery) ([]db.Session, error) {
	return r.SessionsFor(ctx, []Scope{scope}, q)
}

func (r *Reader) SessionsFor(ctx context.Context, scopes []Scope, q platforms.SidebarQuery) ([]db.Session, error) {
	if len(scopes) == 0 {
		return []db.Session{}, nil
	}
	scope := scopes[0]
	if scope.Owner == "" || scope.Platform == "" || q.Limit < 0 || q.Offset < 0 {
		return nil, fmt.Errorf("sidebar: invalid scope or page")
	}
	for _, other := range scopes {
		if other.Owner != scope.Owner || other.Platform == "" {
			return nil, fmt.Errorf("sidebar: mixed owner scope")
		}
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return nil, err
	}
	included, err := json.Marshal(q.IncludeIDs)
	if err != nil {
		return nil, err
	}
	seen, err := json.Marshal(q.SeenCutoffs)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = -1
	}
	rows, err := r.db.QueryContext(ctx, selectSessions, string(raw), string(included), q.UseSeenCutoffs, string(seen), time.Now().UnixMilli(), scope.Owner,
		q.Directory, q.Directory, q.Since, q.Since, q.Origin, q.Origin, q.OriginID, q.OriginID, limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []db.Session{}
	for rows.Next() {
		var row db.Session
		var inferred, role, finish, lastError, liveStatus, mode string
		var archiveAt, projectArchiveAt int64
		var hasArchive, hasSeen, seenInterrupted bool
		if err := rows.Scan(&row.ID, &row.Platform, &row.ProjectID, &row.ParentID, &row.Title, &row.Directory, &row.TimeCreated, &row.TimeUpdated, &row.LastMessageAt, &row.LastTurnCompletedAt, &row.LastUserPromptAt, &row.LastHaltAt, &row.Branch, &row.Origin, &row.OriginID,
			&row.RoutineID, &row.FactoryAttemptID, &inferred, &role, &finish, &lastError, &liveStatus, &row.ResetsAt, &row.LiveConnection, &row.PendingPermission, &row.PendingQuestion,
			&row.PinnedAt, &hasArchive, &archiveAt, &row.SnoozedUntil, &projectArchiveAt, &row.SeenTimeUpdated, &hasSeen, &seenInterrupted, &mode, &row.UnreadCount); err != nil {
			return nil, err
		}
		status := db.SessionStatus(inferred)
		switch status {
		case db.StatusBusy, db.StatusWaiting, db.StatusDone, db.StatusError, db.StatusInterrupted:
		default:
			status = db.InferStoredSessionStatus(role, finish, lastError)
		}
		row.Status = status
		platforms.OverlaySessionObservation(&row, &platforms.LiveState{Status: liveStatus, PendingPermission: row.PendingPermission, PendingQuestion: row.PendingQuestion}, row.LiveConnection)
		row.Pinned = row.PinnedAt > 0
		row.Seen = hasSeen && row.SeenTimeUpdated >= row.TimeUpdated && (row.Status != db.StatusInterrupted || seenInterrupted)
		if row.Seen {
			row.UnreadCount = 0
		}
		row.Archived = hasArchive && (row.TimeUpdated <= archiveAt || (mode != "activity" && row.Status == db.StatusBusy))
		row.Archived = row.Archived || (projectArchiveAt > 0 && row.TimeUpdated <= projectArchiveAt)
		row.RemoteID, row.RemoteName = "local", "This machine"
		out = append(out, row)
	}
	return out, rows.Err()
}
