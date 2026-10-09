package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestHandleSessionsRunningCount(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	busy := mkSession("fake", "archived-busy", "busy", 1)
	busy.Status = db.StatusBusy
	busy.Archived = true
	child := mkSession("fake", "child", "busy", 2)
	child.Status = db.StatusBusy
	child.ParentID = "archived-busy"
	counter := &countingUnreadPlatform{fakePlatform: fakePlatform{id: "fake", sessions: []db.Session{busy, child, mkSession("fake", "done", "done", 3)}}}
	reg.Register(counter)
	remote := mkSession("remote", "busy", "busy", 4)
	remote.Status = db.StatusBusy
	reg.Register(&fakePlatform{id: "remote", sessions: []db.Session{remote}})
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 3 {
		t.Fatalf("count = %d, want 3", result.Count)
	}
	if counter.callCount() != 0 {
		t.Fatal("running count invoked unread enrichment")
	}
	counter.sessions = nil
	reg.Unregister("remote")
	rr = httptest.NewRecorder()
	srv.handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 0 {
		t.Fatalf("empty count = %d", result.Count)
	}
}
