package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestSidebarHTTPSnapshotSharesReadsAndRevalidates(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	var reads atomic.Int32
	reg.Register(&parallelSidebarLocal{
		compactTestPlatform: &compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "local"}},
		read: func(context.Context) ([]db.Session, error) {
			reads.Add(1)
			return []db.Session{{ID: "one", Platform: "local", Title: "Original"}}, nil
		},
	})
	first := httptest.NewRecorder()
	s.handleSidebarSessions(first, httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar&sinceHours=72", nil))
	second := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/sessions?view=sidebar&sinceHours=72", nil)
	r.Header.Set("If-None-Match", first.Header().Get("ETag"))
	s.handleSidebarSessions(second, r)
	if reads.Load() != 1 {
		t.Fatalf("identical browser requests ran %d authoritative reads, want 1", reads.Load())
	}
	if first.Header().Get("ETag") == "" || second.Code != 304 || second.Body.Len() != 0 {
		t.Fatalf("conditional snapshot: etag=%q status=%d body=%s", first.Header().Get("ETag"), second.Code, second.Body)
	}
}
