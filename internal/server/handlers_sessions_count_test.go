package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestHandleSessionsRunningCount(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	busy := mkSession("fake", "archived-busy", "busy", 1)
	busy.Status = db.StatusBusy
	busy.Archived = true
	child := mkSession("fake", "child", "busy", 2)
	child.Status = db.StatusBusy
	child.ParentID = "archived-busy"
	counter := &compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "fake"}, rows: []db.Session{busy, child, mkSession("fake", "done", "done", 3)}}
	reg.Register(counter)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Fatalf("count = %d, want 2", result.Count)
	}
	if counter.calls != 0 {
		t.Fatal("running count loaded sidebar rows")
	}
	counter.rows = nil
	rr = httptest.NewRecorder()
	srv.handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 0 {
		t.Fatalf("empty count = %d", result.Count)
	}
}
