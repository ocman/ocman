package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
)

func TestArchiveCompletedRemoteSetupUsesOwnerWatermark(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	const id = "ocm_01234567-89ab-4cde-8fab-0123456789ab"
	const platform = "r-box:test"
	reg.Register(&fakePlatform{PlatformID: platform, SessionFn: func(string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: platform, TimeUpdated: 101, Status: db.StatusDone}}, nil
	}})
	if _, err := s.stateDB.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,request,created_at) VALUES(?,'{}','ready',?,1)`, id, `{"platform":"r-box:test"}`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	moduletest.Mount(s.sessionLifecycleModule()).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+id+"/archive", nil))
	if w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body.String())
	}
	rows := []db.Session{{ID: id, Platform: platform, TimeUpdated: 101, Status: db.StatusDone}}
	if err := s.applyNotifySessionState(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if !rows[0].Archived {
		raw, _ := json.Marshal(rows)
		t.Fatalf("completed remote setup resurfaced without new activity: %s", raw)
	}
}
