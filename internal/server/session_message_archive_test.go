package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
)

func TestSessionMessageRestoresArchivedSession(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		archived   bool
		snoozed    bool
	}{
		{name: "direct", body: `{"message":"hello"}`, status: http.StatusNoContent},
		{name: "queued", body: `{"message":"hello","queue":true}`, status: http.StatusNoContent},
		{name: "rejected", body: `{"message":""}`, status: http.StatusBadRequest, archived: true},
		{name: "snoozed", body: `{"message":"hello"}`, status: http.StatusNoContent, archived: true, snoozed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, q := newQueueFake(t, true)
			ctx := t.Context()
			if tc.snoozed {
				if err := srv.stateDB.SnoozeSession(ctx, "fake", "s1", time.Now().Add(time.Hour).UnixMilli()); err != nil {
					t.Fatal(err)
				}
			} else if err := srv.stateDB.ArchiveSession(ctx, "fake", "s1", time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if rr := postMessage(t, srv, "s1", tc.body); rr.Code != tc.status {
				t.Fatalf("send status = %d, want %d: %s", rr.Code, tc.status, rr.Body)
			}
			if tc.name == "queued" && len(q.sentMessages()) != 0 {
				t.Fatal("expected session to be restored before queued message delivery")
			}
			rr := httptest.NewRecorder()
			serveSessionRoute(srv.handleSession, rr, httptest.NewRequest(http.MethodGet, "/api/session/s1?platform=fake&peek=1", nil))
			var detail struct{ Session db.Session }
			if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &detail) != nil {
				t.Fatalf("read session: %d %s", rr.Code, rr.Body)
			}
			if detail.Session.Archived != tc.archived || (detail.Session.SnoozedUntil > 0) != tc.snoozed {
				t.Fatalf("archived=%v, snoozedUntil=%d; want archived=%v, snoozed=%v", detail.Session.Archived, detail.Session.SnoozedUntil, tc.archived, tc.snoozed)
			}
		})
	}
}
