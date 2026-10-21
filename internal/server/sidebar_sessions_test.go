package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type compactTestPlatform struct {
	*fakePlatform
	calls   int
	query   platforms.SidebarQuery
	sources []platforms.SidebarSource
	rows    []db.Session
}

func (p *compactTestPlatform) SidebarSource(context.Context) (platforms.SidebarSource, error) {
	return platforms.SidebarSource{Owner: "owner", Platform: string(p.ID()), Source: "source", Generation: "g"}, nil
}
func (p *compactTestPlatform) SidebarSessionsFor(_ context.Context, sources []platforms.SidebarSource, q platforms.SidebarQuery) ([]db.Session, error) {
	p.calls++
	p.query = q
	p.sources = sources
	return p.rows, nil
}
func (p *compactTestPlatform) SidebarSessions(context.Context, platforms.SidebarQuery) ([]db.Session, error) {
	panic("owner-local platforms must be combined")
}

func TestSidebarEndpointCombinesPlatformsWithoutSummaryOrStateFanout(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := &compactTestPlatform{fakePlatform: &fakePlatform{id: "first", sessionsHook: func(context.Context, string, int64) ([]db.Session, error) {
		t.Fatal("legacy list used")
		return nil, nil
	}}, rows: []db.Session{{ID: "one", Platform: "first", TimeUpdated: 30, Status: db.StatusBusy, Origin: "routine", RoutineID: "routine-1", Pinned: true}, {ID: "two", Platform: "second", TimeUpdated: 20, Status: db.StatusDone, Seen: true}}}
	other := &compactTestPlatform{fakePlatform: &fakePlatform{id: "second"}}
	other.rows = p.rows
	reg.Register(p)
	reg.Register(other)
	r := httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar&origin=routine&originId=routine-1&limit=1&offset=1&since=10&dir=/repo", nil)
	w := httptest.NewRecorder()
	s.handleSessions(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []db.Session
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].ID != "two" || !got[0].Seen {
		t.Fatalf("page/flags changed: %+v %v", got, err)
	}
	// Registry iteration is deterministic but either local adapter may be the
	// group reader. Both must never run a separate query for the same owner.
	if p.calls+other.calls != 1 {
		t.Fatalf("used %d owner queries", p.calls+other.calls)
	}
	selected := p
	if other.calls > 0 {
		selected = other
	}
	if len(selected.sources) != 2 || selected.query.Origin != "routine" || selected.query.OriginID != "routine-1" || selected.query.Limit != 2 || selected.query.Offset != 0 || selected.query.Since != 10 || selected.query.Directory != "/repo" {
		t.Fatalf("query=%+v sources=%+v", selected.query, selected.sources)
	}
}

func TestSidebarEndpointCountsRunningAndRejectsBadPages(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := &compactTestPlatform{fakePlatform: &fakePlatform{id: "test"}, rows: []db.Session{{ID: "one", Status: db.StatusBusy}, {ID: "two", Status: db.StatusDone}}}
	reg.Register(p)
	w := httptest.NewRecorder()
	s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
	var result struct{ Count int }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Count != 1 || p.query.Limit != 0 {
		t.Fatalf("count=%+v %v body=%s", result, err, w.Body.String())
	}
	for _, query := range []string{"limit=-1", "offset=-1", "limit=10001", "offset=1000001"} {
		w := httptest.NewRecorder()
		s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar&"+query, nil))
		if w.Code != 400 {
			t.Fatalf("%s status=%d", query, w.Code)
		}
	}
}

type remoteCompactTestPlatform struct {
	*fakePlatform
	query platforms.SidebarQuery
	rows  []db.Session
}

func (p *remoteCompactTestPlatform) SidebarSessions(_ context.Context, q platforms.SidebarQuery) ([]db.Session, error) {
	p.query = q
	return p.rows, nil
}

func TestSidebarKeepsHubRemotePinsAndOriginTags(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	platform := "r-remote:test"
	p := &remoteCompactTestPlatform{fakePlatform: &fakePlatform{id: platform}, rows: []db.Session{{ID: "old", Platform: platform, RemoteID: "remote", Directory: "/repo", TimeUpdated: 2, SeenTimeUpdated: 1, UnreadCount: 1, Status: db.StatusDone, Origin: "factory", FactoryAttemptID: "attempt"}}}
	reg.Register(p)
	if err := s.stateDB.PinSession(t.Context(), platform, "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.MarkSessionSeen(t.Context(), platform, "old", 1); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar&since=100&limit=0", nil))
	var rows []db.Session
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || w.Code != 200 || len(rows) != 1 || !rows[0].Pinned || rows[0].FactoryAttemptID != "attempt" || rows[0].Seen || rows[0].UnreadCount != 1 {
		t.Fatalf("hub flags/tags=%+v %v body=%s", rows, err, w.Body.String())
	}
	if len(p.query.IncludeIDs) != 1 || p.query.IncludeIDs[0] != "old" || !p.query.UseSeenCutoffs || p.query.SeenCutoffs["old"] != 1 {
		t.Fatalf("owner query lost hub preferences: %+v", p.query)
	}
}
