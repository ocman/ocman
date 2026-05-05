package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// stateSetup describes the state.db rows a test wants pre-populated
// before /api/sessions is invoked. Each entry is a (platform,
// sessionID, asOfTimestamp) triple. The timestamp matters for seen
// (only-if-newer semantics) and archive (unarchive-on-newer-update
// semantics).
type stateSetup struct {
	archived []stateRow
	seen     []stateRow
	pinned   []stateRow
}

type stateRow struct {
	platform string
	id       string
	at       int64 // ms; ignored for pinned
}

// newSessionsTestServer returns a Server with no real OpenCode DB
// (handleSessions doesn't need it — the dashboard data path goes
// through the registry's adapters, not s.db) and a fresh on-disk
// state.db. Tests register a fakePlatform via the returned registry.
func newSessionsTestServer(t *testing.T) (*Server, *platforms.Registry) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "state.db")
	stDB, err := state.Open(tmp)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { stDB.Close() })

	reg := platforms.NewRegistry()
	srv := New(nil, stDB, "127.0.0.1:0", reg, nil)
	return srv, reg
}

func applyStateSetup(t *testing.T, db *state.DB, setup stateSetup) {
	t.Helper()
	for _, r := range setup.archived {
		if err := db.ArchiveSession(r.platform, r.id, r.at); err != nil {
			t.Fatalf("ArchiveSession(%v): %v", r, err)
		}
	}
	for _, r := range setup.seen {
		if err := db.MarkSessionSeen(r.platform, r.id, r.at); err != nil {
			t.Fatalf("MarkSessionSeen(%v): %v", r, err)
		}
	}
	for _, r := range setup.pinned {
		if err := db.PinSession(r.platform, r.id); err != nil {
			t.Fatalf("PinSession(%v): %v", r, err)
		}
	}
}

func mkSession(platform, id, title string, updated int64) db.Session {
	return db.Session{
		ID:          id,
		Platform:    platform,
		Title:       title,
		TimeUpdated: updated,
		TimeCreated: updated - 1000,
		ProjectID:   "proj-" + id,
	}
}

func TestHandleSessions_EmptyRegistry(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got []db.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body)
	}
	if len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestHandleSessions_SinglePlatform_SortedByBucketDesc(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	// Three sessions, two in the same 5-minute bucket (bucketMs =
	// 300000) and one earlier. The two in the same bucket sort by
	// projectID then title (stable secondary keys).
	reg.Register(&fakePlatform{
		id: "fake",
		sessions: []db.Session{
			mkSession("fake", "a", "alpha", 600000), // bucket 2
			mkSession("fake", "b", "beta", 700000),  // bucket 2
			mkSession("fake", "c", "gamma", 100000), // bucket 0
		},
	})
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}
	// "gamma" (bucket 0) must be last; the other two are in bucket 2,
	// sorted by ProjectID ("proj-a" < "proj-b") then title.
	if got[2].ID != "c" {
		t.Fatalf("last session id = %q, want c", got[2].ID)
	}
}

func TestHandleSessions_TwoPlatforms_Merged(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:       "p1",
		sessions: []db.Session{mkSession("p1", "x", "alpha", 1_000_000)},
	})
	reg.Register(&fakePlatform{
		id:       "p2",
		sessions: []db.Session{mkSession("p2", "y", "alpha", 1_000_500)},
	})
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	// Both in the same bucket, sorted by ProjectID ("proj-x" vs "proj-y").
	if got[0].ID != "x" || got[1].ID != "y" {
		t.Fatalf("merge order = %q,%q; want x,y", got[0].ID, got[1].ID)
	}
}

func TestHandleSessions_StateOverlay_AppliesArchivedSeenPinned(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id: "fake",
		sessions: []db.Session{
			mkSession("fake", "arch", "a", 1000),
			mkSession("fake", "seen", "b", 1000),
			mkSession("fake", "pin", "c", 1000),
		},
	})
	applyStateSetup(t, srv.stateDB, stateSetup{
		archived: []stateRow{{"fake", "arch", 2000}}, // archivedAt > timeUpdated → stays archived
		seen:     []stateRow{{"fake", "seen", 1000}}, // seenAt >= timeUpdated → seen
		pinned:   []stateRow{{"fake", "pin", 0}},
	})

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	byID := map[string]db.Session{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if !byID["arch"].Archived {
		t.Errorf("session arch: expected Archived=true")
	}
	if !byID["seen"].Seen {
		t.Errorf("session seen: expected Seen=true")
	}
	if !byID["pin"].Pinned {
		t.Errorf("session pin: expected Pinned=true")
	}
}

func TestHandleSessions_LimitParameterRespected(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var sessions []db.Session
	for i := 0; i < 10; i++ {
		sessions = append(sessions, mkSession("fake", fmt.Sprintf("s%d", i), "t", int64(1000+i*1000)))
	}
	reg.Register(&fakePlatform{id: "fake", sessions: sessions})

	req := httptest.NewRequest("GET", "/api/sessions?limit=3", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3 (limit)", len(got))
	}
}

func TestHandleSessions_SinceParameterIsForwardedToAdapter(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var seenSince int64
	reg.Register(&fakePlatform{
		id: "fake",
		sessionsHook: func(_ context.Context, dir string, since int64) ([]db.Session, error) {
			seenSince = since
			return nil, nil
		},
	})

	req := httptest.NewRequest("GET", "/api/sessions?since=12345", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if seenSince != 12345 {
		t.Fatalf("adapter Sessions called with since=%d, want 12345", seenSince)
	}
}

func TestHandleSessions_PinnedOutsideWindow_IsForceIncluded(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	// The adapter normally returns nothing within the since window
	// but exposes the pinned session via Session()/Owns().
	pinnedSess := mkSession("fake", "pinme", "old session", 50)
	reg.Register(&fakePlatformWithDetail{
		fakePlatform: fakePlatform{
			id:       "fake",
			sessions: nil, // empty within window
		},
		detailSession: &pinnedSess,
	})
	applyStateSetup(t, srv.stateDB, stateSetup{
		pinned: []stateRow{{"fake", "pinme", 0}},
	})

	req := httptest.NewRequest("GET", "/api/sessions?since=10000", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].ID != "pinme" {
		t.Fatalf("force-include of pinned session failed; got %+v", got)
	}
	if !got[0].Pinned {
		t.Errorf("force-included pinned session must have Pinned=true")
	}
}

func TestHandleSessions_PlatformErrorDoesNotAbortRequest(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:          "broken",
		sessionsErr: errors.New("upstream down"),
	})
	reg.Register(&fakePlatform{
		id:       "ok",
		sessions: []db.Session{mkSession("ok", "z", "z", 1000)},
	})

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].ID != "z" {
		t.Fatalf("expected only the healthy platform's session; got %+v", got)
	}
}

// fakePlatformWithDetail extends fakePlatform with a non-nil
// SessionDetail returned from Session(). Used to exercise the
// pinned-force-include branch of handleSessions, which calls
// adapter.Session() to fetch a session that was pinned but not
// returned by Sessions() (e.g. it fell outside the time window).
type fakePlatformWithDetail struct {
	fakePlatform
	detailSession *db.Session
}

func (f *fakePlatformWithDetail) Session(_ context.Context, sessionID string, _, _ int) (*platforms.SessionDetail, error) {
	if f.detailSession != nil && f.detailSession.ID == sessionID {
		return &platforms.SessionDetail{Session: f.detailSession}, nil
	}
	return nil, platforms.ErrNotFound
}

func (f *fakePlatformWithDetail) Owns(_ context.Context, sessionID string) bool {
	if f.detailSession != nil && f.detailSession.ID == sessionID {
		return true
	}
	return f.fakePlatform.Owns(context.Background(), sessionID)
}

func mustUnmarshal(t *testing.T, data []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, string(data))
	}
}
