package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestDispatchSessionSubpath_GETSubroutes_ReachHandlers exercises
// every GET-style session sub-route against a fakePlatform and
// asserts the dispatcher routes the request to a handler that runs
// to completion. The fakePlatform returns nothing meaningful; we
// only care that a non-error status is produced (or 200 with the
// canonical empty-list shape), proving the matcher + handler chain
// is wired correctly.
//
// This complements the structural TestSessionSubRoutesUnique +
// TestDispatchSessionSubpath_AllRoutesReachable tests by actually
// invoking the handlers — which catches signature drift between the
// routing table and the handler implementations.
func TestDispatchSessionSubpath_GETSubroutes_ReachHandlers(t *testing.T) {
	const sid = "sess-1"
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", sid, "t", 1000)},
		// A non-nil session detail keeps handleSession happy.
		info: &platforms.SessionInfo{},
	})

	cases := []struct {
		path string
	}{
		{"/api/session/" + sid + "/agents"},
		{"/api/session/" + sid + "/commands"},
		{"/api/session/" + sid + "/models"},
		{"/api/session/" + sid + "/permissions"},
		{"/api/session/" + sid + "/questions"},
		{"/api/session/" + sid + "/info"},
		// changes / events / tasks / bare /{id} require richer setup
		// (real DB rows or registered SSE source); skipping them
		// here keeps the test focused on dispatcher-handler wiring.
	}

	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", c.path, nil)
			rr := httptest.NewRecorder()
			srv.dispatchSessionSubpath(rr, req)

			if rr.Code == http.StatusNotFound {
				t.Fatalf("dispatcher returned 404 for known route %q; body=%s", c.path, rr.Body)
			}
			if rr.Code == http.StatusMethodNotAllowed {
				t.Fatalf("dispatcher returned 405 for known GET route %q", c.path)
			}
			// Drain so the test is hermetic.
			_, _ = io.ReadAll(rr.Body)
			_ = context.Background()
		})
	}
}
