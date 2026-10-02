package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestArchiveResurfaceSettings(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	for _, tc := range []struct {
		method, body, want string
		status             int
	}{
		{http.MethodGet, "", `"mode":"halt"`, 200},
		{http.MethodPost, `{"mode":"activity"}`, `"mode":"activity"`, 200},
		{http.MethodGet, "", `"mode":"activity"`, 200},
		{http.MethodPost, `{"mode":"halt"}`, `"mode":"halt"`, 200},
		{http.MethodPost, `{"mode":"unknown"}`, "", 400},
		{http.MethodPost, `{`, "", 400},
		{http.MethodDelete, "", "", 405},
	} {
		rec := httptest.NewRecorder()
		srv.handleArchiveResurface(rec, httptest.NewRequest(tc.method, "/api/settings/archive-resurface", strings.NewReader(tc.body)))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.body, rec.Code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	(&Server{}).handleArchiveResurface(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 503 {
		t.Fatalf("missing DB: %d", rec.Code)
	}
}

func TestArchiveResurfaceOverlay(t *testing.T) {
	for _, mode := range []string{"halt", "activity"} {
		for _, status := range []db.SessionStatus{db.StatusBusy, db.StatusDone, db.StatusError, db.StatusWaiting, db.StatusInterrupted} {
			for _, path := range []string{"list", "notify", "read-only"} {
				t.Run(mode+"/"+string(status)+"/"+path, func(t *testing.T) {
					srv := &Server{stateDB: openTestStateDB(t), registry: platforms.NewRegistry()}
					ctx := t.Context()
					if mode == "activity" {
						if err := srv.stateDB.SetSetting(ctx, archiveResurfaceKey, mode); err != nil {
							t.Fatal(err)
						}
					}
					if err := srv.stateDB.ArchiveSession(ctx, "fake", "s1", 100); err != nil {
						t.Fatal(err)
					}
					apply := srv.applySessionState
					switch path {
					case "notify":
						apply = srv.applyNotifySessionState
					case "read-only":
						apply = srv.applySessionStateReadOnly
					}
					for _, updated := range []int64{100, 101} {
						sessions := []db.Session{{ID: "s1", Platform: "fake", TimeUpdated: updated, Status: status}}
						if err := apply(ctx, sessions); err != nil {
							t.Fatal(err)
						}
						resurface := updated > 100 && (mode == "activity" || status != db.StatusBusy)
						if path != "notify" && sessions[0].Archived == resurface {
							t.Fatalf("updated=%d archived=%v, resurface=%v", updated, sessions[0].Archived, resurface)
						}
						archived, err := srv.stateDB.ArchivedSessions(ctx)
						if err != nil {
							t.Fatal(err)
						}
						_, stored := archived[state.Key{Platform: "fake", SessionID: "s1"}]
						if stored != (!resurface || path == "read-only") {
							t.Fatalf("updated=%d stored archive=%v", updated, stored)
						}
					}
				})
			}
		}
	}
}
