package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func (b *backend) serveRead(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	path, q := r.URL.Path, r.URL.Query()
	var v any
	var err error
	switch {
	case path == "/sessions" && q.Get("live") != "":
		v = b.statuses()
	case path == "/sessions" && q.Get("runtime") != "":
		v = b.runtime(q.Get("directory"))
	case path == "/sessions":
		since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
		v, err = b.list(ctx, q.Get("directory"), since)
	case path == "/catalog":
		v, err = b.catalog(ctx, q.Get("directory"))
	case path == "/permissions" || path == "/questions":
		v = b.pendingPrompts(q.Get("sessionID"), path == "/questions")
	case strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/ownership"):
		err = platforms.ErrUnsupported
	case strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/lifecycle"):
		v, err = b.lifecycle(ctx, strings.TrimSuffix(strings.TrimPrefix(path, "/sessions/"), "/lifecycle"))
	case strings.HasPrefix(path, "/sessions/") && !strings.Contains(strings.TrimPrefix(path, "/sessions/"), "/"):
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		v, err = b.detail(ctx, strings.TrimPrefix(path, "/sessions/"), limit, offset)
	default:
		writeJSON(w, http.StatusNotImplemented, providerprotocol.Error{Code: "unsupported", Message: "unsupported operation"})
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
			out = append(out, db.Session{ID: id, Platform: PlatformID, Title: s.title, Directory: s.dir, Status: b.statusLocked(id, ""), LiveConnection: true})
		}
	}
	return out
}

func (b *backend) list(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	c, _, err := b.conn(ctx)
	if err != nil {
		return nil, err
	}
	threads, err := c.ListThreads(ctx, codexappserver.ThreadListParams{SourceKinds: codexappserver.AllSourceKinds, Cwd: dir,
		SortKey: "updated_at", SortDirection: "desc"})
	if err != nil {
		return nil, err
	}
	out := []db.Session{}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range threads {
		t := &threads[i]
		s := Session(t)
		if t.Ephemeral || s.TimeUpdated < since {
			continue
		}
		inferred := db.StatusDone
		if t.Status.Type == "active" {
			inferred = db.StatusBusy
		}
		s.Status, s.LiveConnection = b.statusLocked(t.ID, inferred), true
		out = append(out, s)
	}
	return out, nil
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

// detail pages like db.PaginateMessages: offset counts from the newest.
func (b *backend) detail(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	snap, err := b.history(ctx, id)
	if err != nil {
		return nil, err
	}
	total := len(snap.Messages)
	if limit <= 0 {
		limit = total
	}
	end := total - min(max(offset, 0), total)
	start := end - min(limit, end)
	page := map[string]bool{}
	messages := []db.Message{}
	for _, m := range snap.Messages[start:end] {
		page[m.ID] = true
		messages = append(messages, m)
	}
	parts := []db.Part{}
	for _, p := range snap.Parts {
		if page[p.MessageID] {
			parts = append(parts, p)
		}
	}
	s := snap.Session
	b.mu.Lock()
	s.Status, s.LiveConnection = b.statusLocked(id, snap.InferredStatus), true
	b.mu.Unlock()
	return &platforms.SessionDetail{Session: &s, SessionTree: []db.Session{s}, Messages: messages, Parts: parts, TotalMessages: total}, nil
}

func (b *backend) lifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	snap, err := b.history(ctx, id)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	out := &platforms.SessionLifecycle{Status: b.statusLocked(id, snap.InferredStatus)}
	b.mu.Unlock()
	if n := len(snap.Messages); n > 0 {
		last := snap.Messages[n-1]
		var info struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(last.Data, &info)
		out.LatestMessageID, out.LatestMessageCreated, out.LatestMessageRole = last.ID, last.TimeCreated, info.Role
	}
	return out, nil
}
