package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestInterruptedSessionBecomesUnreadWithoutNewActivity(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	row := &db.Session{ID: "crashed", Platform: "fake", TimeUpdated: 100, Status: db.StatusBusy}
	p := &fakePlatformWithDetail{fakePlatform: fakePlatform{id: "fake", sessions: []db.Session{*row}}, detailSession: row}
	reg.Register(p)
	mark := func() {
		t.Helper()
		w := httptest.NewRecorder()
		srv.handleSeenSession(w, httptest.NewRequest(http.MethodPost, "/api/session/seen", strings.NewReader(fmt.Sprintf(`{"platform":"fake","sessionId":"crashed","timeUpdated":100,"interrupted":%t}`, row.Status == db.StatusInterrupted))))
		if w.Code != 200 {
			t.Fatalf("mark seen: %d %s", w.Code, w.Body.String())
		}
	}
	check := func(want bool) {
		t.Helper()
		for _, overlay := range []func() ([]db.Session, error){
			func() ([]db.Session, error) {
				rows := []db.Session{*row}
				return rows, srv.applySessionState(t.Context(), rows)
			},
			func() ([]db.Session, error) {
				rows := []db.Session{*row}
				return rows, srv.applyNotifySessionState(t.Context(), rows)
			},
		} {
			rows, err := overlay()
			if err != nil {
				t.Fatal(err)
			}
			if rows[0].Seen != want {
				t.Errorf("status %s: seen = %v, want %v", row.Status, rows[0].Seen, want)
			}
		}
	}
	mark()
	check(true)
	row.Status = db.StatusInterrupted
	check(false)
	p.sessions = []db.Session{*row}
	w := httptest.NewRecorder()
	srv.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"crashed"`) {
		t.Fatalf("unread interruption missing from notifications: %d %s", w.Code, w.Body.String())
	}
	mark()
	check(true)
	w = httptest.NewRecorder()
	srv.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil))
	if strings.Contains(w.Body.String(), `"id":"crashed"`) {
		t.Fatalf("acknowledged interruption still notifies: %s", w.Body.String())
	}
	row.Status = db.StatusBusy
	mark()
	row.Status = db.StatusInterrupted
	check(false)
}
