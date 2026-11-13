package codexprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor"
)

func (b *backend) serveRead(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	path, q := r.URL.Path, r.URL.Query()
	var v any
	var err error
	switch {
	case path == "/catalog":
		v = &platforms.DirectoryCatalog{Commands: b.acp.directoryCommands(q.Get("directory"))}
	case path == "/sessions" && q.Get("live") != "":
		v = b.statuses()
	case path == "/sessions" && q.Get("runtime") != "":
		v = b.runtime(q.Get("directory"))
	case path == "/permissions" || path == "/questions":
		v = b.pendingPrompts(q.Get("sessionID"), path == "/questions")
	case strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/lifecycle"):
		v, err = b.lifecycle(ctx, strings.TrimSuffix(strings.TrimPrefix(path, "/sessions/"), "/lifecycle"))
	default:
		providersupervisor.WriteJSON(w, http.StatusNotImplemented, providerprotocol.Error{Code: "unsupported", Message: "unsupported operation"})
		return
	}
	reply(w, v, err)
}

// statuses lists the sessions whose status this supervisor settled.
func (b *backend) statuses() []db.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []db.Session{}
	for id, s := range b.sessions {
		if s.status != "" {
			out = append(out, db.Session{ID: id, Status: s.status})
		}
	}
	return out
}

// runtime lists the sessions this supervisor drove or created.
func (b *backend) runtime(dir string) []db.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []db.Session{}
	for id, s := range b.sessions {
		if s.dir != "" && (dir == "" || dir == s.dir) {
			row := s.metadata
			row.ID, row.Platform, row.Title, row.Directory = id, PlatformID, s.title, s.dir
			row.Status, row.LiveConnection = b.statusLocked(id, ""), true
			out = append(out, row)
		}
	}
	return out
}

// statusLocked is the session's settled status: this supervisor's own when
// it drove a turn, else the stored inference settled as unobserved.
func (b *backend) statusLocked(id string, inferred db.SessionStatus) db.SessionStatus {
	if s := b.sessions[id]; s != nil {
		if s.running {
			return db.StatusBusy
		}
		if s.status != "" {
			return s.status
		}
	}
	if inferred == "" {
		inferred = db.StatusDone
	}
	return db.SettleSessionStatus(db.TurnUnobserved, false, inferred)
}

// history reads a thread's stored conversation. A thread this supervisor
// created that has no stored history yet reads as empty.
func (b *backend) history(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	c, _, err := b.conn(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := (&HistoryReader{Client: c}).Snapshot(ctx, id)
	if errors.Is(err, platforms.ErrNotFound) {
		b.mu.Lock()
		var dir, title string
		if s := b.sessions[id]; s != nil {
			dir, title = s.dir, s.title
		}
		b.mu.Unlock()
		if dir != "" {
			return &db.HistorySnapshot{Session: db.Session{ID: id, Platform: PlatformID, Title: title, Directory: dir},
				InferredStatus: db.StatusDone, Messages: []db.Message{}, Parts: []db.Part{}}, nil
		}
	}
	return snap, err
}

func (b *backend) lifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	snap, err := b.history(ctx, id)
	if err != nil {
		return nil, err
	}
	out := StoredLifecycle(snap)
	b.mu.Lock()
	out.Status = b.statusLocked(id, snap.InferredStatus)
	b.mu.Unlock()
	return out, nil
}
