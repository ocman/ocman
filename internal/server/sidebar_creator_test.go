package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/state"
)

func TestSidebarReturnsSavedCreator(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	reg.Register(&compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "opencode"}, rows: []db.Session{
		{ID: "child", Platform: "opencode", Directory: "/other/project", Status: db.StatusDone},
		{ID: "parent", Platform: "opencode", Directory: "/project"},
	}})
	if err := s.stateDB.RecordSessionCreator(t.Context(), state.SessionCreator{Platform: "opencode", SessionID: "child", CreatorPlatform: "opencode", CreatorSessionID: "parent", CreatedAt: 42}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar&limit=0", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var rows []struct {
		ID          string `json:"id"`
		ParentID    string `json:"parentId"`
		SourceChild bool   `json:"sourceChild"`
		Creator     *struct {
			Platform  string `json:"platform"`
			SessionID string `json:"sessionId"`
		} `json:"creator"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "child" {
			if row.Creator == nil || row.Creator.Platform != "opencode" || row.Creator.SessionID != "parent" || row.ParentID != "" || row.SourceChild {
				t.Fatalf("creator missing or native ancestry changed: %s", w.Body.String())
			}
			return
		}
	}
	t.Fatal("missing child")
}

func TestSidebarCreatorScopeAndErrors(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	if err := s.stateDB.RecordSessionCreator(t.Context(), state.SessionCreator{Platform: "remote:opencode", SessionID: "ocm_child", CreatorPlatform: "claude", CreatorSessionID: "ocm_parent"}); err != nil {
		t.Fatal(err)
	}
	rows := []db.Session{{ID: "ocm_child", Platform: "opencode"}, {ID: "ocm_child", Platform: "remote:opencode"}}
	if err := s.enrichSidebarCreators(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Creator != nil || rows[1].Creator == nil || rows[1].Creator.SessionID != "ocm_parent" || rows[1].Creator.Platform != "claude" {
		t.Fatalf("machine scope lost: %#v", rows)
	}
	if err := s.enrichSidebarCreators(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.stateDB.SQL().Exec(`DROP TABLE session_creator`); err != nil {
		t.Fatal(err)
	}
	if err := s.enrichSidebarCreators(t.Context(), rows); err == nil {
		t.Fatal("lost query error")
	}
	s.stateDB = nil
	if err := s.enrichSidebarCreators(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
}
