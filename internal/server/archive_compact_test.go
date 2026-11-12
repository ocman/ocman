package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestCompactEndpointKeepsArchivedSetupWithoutPRWatch(t *testing.T) {
	for _, platform := range []string{"test", "r-remote:test"} {
		t.Run(platform, func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			rows := []db.Session{{ID: "setup", Platform: platform, RemoteID: "local", TimeUpdated: 101, Status: db.StatusDone}}
			if platform == "test" {
				reg.Register(&compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: platform}, rows: rows})
			} else {
				rows[0].RemoteID = "remote"
				reg.Register(&remoteCompactTestPlatform{fakePlatform: &fakePlatform{PlatformID: platform}, rows: rows})
			}
			if _, err := s.stateDB.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,created_at) VALUES('setup','{}','delivering',1)`); err != nil {
				t.Fatal(err)
			}
			if err := s.stateDB.ArchiveSession(t.Context(), platform, "setup", 100); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar", nil))
			var got []db.Session
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || !got[0].Archived {
				t.Fatalf("status=%d rows=%+v err=%v body=%s", w.Code, got, err, w.Body.String())
			}
		})
	}
}
