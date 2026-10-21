package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type failedNotificationSidebar struct{ *remoteCompactTestPlatform }

func (p *failedNotificationSidebar) SidebarSessions(context.Context, platforms.SidebarQuery) ([]db.Session, error) {
	return nil, errors.New("remote unavailable")
}

func TestBrowserNotificationsKeepHealthyCompactRowsWhenRemoteFails(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	reg.Register(&compactTestPlatform{fakePlatform: &fakePlatform{id: "local", sessionsHook: func(context.Context, string, int64) ([]db.Session, error) {
		t.Fatal("notification reader fell back to the native session list")
		return nil, nil
	}}, rows: []db.Session{{ID: "healthy", Platform: "local", Status: db.StatusWaiting, UnreadCount: 1, TimeUpdated: 100}}})
	reg.Register(&failedNotificationSidebar{remoteCompactTestPlatform: &remoteCompactTestPlatform{fakePlatform: &fakePlatform{id: "r-failed:local"}}})
	rows, err := s.notificationSessionRows(t.Context(), 0, 500)
	if err != nil || len(rows) != 1 || rows[0].ID != "healthy" {
		t.Fatalf("one failed remote suppressed healthy browser notifications: %+v %v", rows, err)
	}
	w := httptest.NewRecorder()
	s.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("browser notification endpoint failed: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.pushSnapshot(t.Context()); err == nil {
		t.Fatal("push delivery accepted an incomplete owner snapshot")
	}
}
