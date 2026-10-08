package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestPeekReturnsInterruptionReadState(t *testing.T) {
	for _, platform := range []string{"opencode", "r-box:opencode"} {
		for _, acknowledged := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", platform, acknowledged), func(t *testing.T) {
				srv, _ := newPeekTestServer(t, platform, db.StatusInterrupted)
				if err := srv.stateDB.MarkSessionSeen(t.Context(), platform, "s1", 2000, acknowledged); err != nil {
					t.Fatal(err)
				}
				if err := srv.stateDB.MarkSessionSeen(t.Context(), "other", "s1", 3000, !acknowledged); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				srv.handleSession(w, httptest.NewRequest(http.MethodGet, "/api/session/s1?peek=1&platform="+platform, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("peek: %d %s", w.Code, w.Body.String())
				}
				var body struct{ Session db.Session }
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Session.Seen != acknowledged || body.Session.SeenTimeUpdated != 2000 {
					t.Fatalf("peek read state: seen=%v updated=%d, want %v/2000", body.Session.Seen, body.Session.SeenTimeUpdated, acknowledged)
				}
			})
		}
	}
}

func TestInterruptedSessionBecomesUnreadWithoutNewActivity(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	row := &db.Session{ID: "crashed", Platform: "fake", TimeUpdated: 100, Status: db.StatusBusy}
	p := &fakePlatformWithDetail{fakePlatform: fakePlatform{id: "fake", sessions: []db.Session{*row}}, detailSession: row}
	reg.Register(p)
	mark := func() {
		t.Helper()
		w := httptest.NewRecorder()
		srv.handleSeenSession(w, httptest.NewRequest(http.MethodPost, "/api/session/seen", strings.NewReader(fmt.Sprintf(`{"platform":"fake","sessionId":"crashed","timeUpdated":%d,"interrupted":%t}`, row.TimeUpdated, row.Status == db.StatusInterrupted))))
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
	srv.handleSeenSession(w, httptest.NewRequest(http.MethodPost, "/api/session/seen", strings.NewReader(`{"platform":"fake","sessionId":"crashed","timeUpdated":100}`)))
	check(true)
	w = httptest.NewRecorder()
	srv.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil))
	if strings.Contains(w.Body.String(), `"id":"crashed"`) {
		t.Fatalf("acknowledged interruption still notifies: %s", w.Body.String())
	}
	row.Status = db.StatusBusy
	row.TimeUpdated++
	mark()
	row.Status = db.StatusInterrupted
	check(false)
}
