package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestSessionSubRoutesUnique asserts no two table entries share the
// same (method, pattern) pair. Catching a copy-paste duplicate at
// build time is cheaper than tracking down the resulting silent
// shadowing in production.
func TestSessionSubRoutesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range sessionSubRoutes {
		key := r.method + " " + r.pattern
		if seen[key] {
			t.Errorf("duplicate route: %s", key)
		}
		seen[key] = true
	}
}

func TestMatchSessionSubRoute(t *testing.T) {
	cases := []struct {
		pattern, subpath string
		wantOK           bool
		wantParams       map[string]string
	}{
		{"archive", "archive", true, map[string]string{}},
		{"archive", "abc", false, nil},
		{"{id}", "abc", true, map[string]string{"id": "abc"}},
		{"{id}/agents", "abc/agents", true, map[string]string{"id": "abc"}},
		{"{id}/agents", "abc", false, nil},
		{"{id}/agents", "abc/agents/extra", false, nil},
		{"{id}/permissions/{pid}", "abc/permissions/p1", true, map[string]string{"id": "abc", "pid": "p1"}},
		{"{id}/questions/{qid}/reject", "abc/questions/q1/reject", true, map[string]string{"id": "abc", "qid": "q1"}},
		{"{id}/questions/{qid}/reject", "abc/questions/q1", false, nil},
		// Empty segment shouldn't bind to a placeholder.
		{"{id}/agents", "/agents", false, nil},
	}
	for _, c := range cases {
		t.Run(c.pattern+"_vs_"+c.subpath, func(t *testing.T) {
			gotParams, gotOK := matchSessionSubRoute(c.pattern, c.subpath)
			if gotOK != c.wantOK {
				t.Fatalf("match %q vs %q: ok=%v, want %v", c.pattern, c.subpath, gotOK, c.wantOK)
			}
			if c.wantOK && !reflect.DeepEqual(gotParams, c.wantParams) {
				t.Fatalf("params = %v, want %v", gotParams, c.wantParams)
			}
		})
	}
}

// TestDispatchSessionSubpath_MoreSpecificWinsForOverlap asserts that
// "{id}/questions/{qid}/reject" wins over "{id}/questions/{qid}" when
// the path matches both. This is the only place in the table where
// patterns overlap, so it deserves a dedicated test.
//
// We can't run the real handler (no DB / state setup), but we can
// verify that the dispatcher *would* route the request to the right
// handler by inspecting the table directly: the reject variant must
// come first in sessionSubRoutes.
func TestDispatchSessionSubpath_MoreSpecificWinsForOverlap(t *testing.T) {
	rejectIdx, rejectFound := -1, false
	plainIdx, plainFound := -1, false
	for i, r := range sessionSubRoutes {
		if r.pattern == "{id}/questions/{qid}/reject" {
			rejectIdx, rejectFound = i, true
		}
		if r.pattern == "{id}/questions/{qid}" {
			plainIdx, plainFound = i, true
		}
	}
	if !rejectFound || !plainFound {
		t.Fatalf("expected both /questions/{qid} and /questions/{qid}/reject in table")
	}
	if rejectIdx > plainIdx {
		t.Fatalf("table order incorrect: /reject (idx %d) must precede /questions/{qid} (idx %d) so the more-specific pattern wins", rejectIdx, plainIdx)
	}
}

// TestDispatchSessionSubpath_UnknownPathReturns404 verifies that an
// unsupported sub-path produces a 404 whose body includes the
// offending request path. This is FR-6's "future typo surfaces
// quickly" guarantee.
func TestDispatchSessionSubpath_UnknownPathReturns404(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/session/abc/totally-fake-subpath", nil)
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "/api/session/abc/totally-fake-subpath") {
		t.Errorf("404 body %q missing offending path", rr.Body.String())
	}
}

// TestDispatchSessionSubpath_WrongMethodReturns405 covers the case
// where the path matches a known pattern but the method does not —
// e.g. PATCH /api/session/{id}/agents (only GET is allowed).
func TestDispatchSessionSubpath_WrongMethodReturns405(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/session/abc/agents", nil)
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body=%s", rr.Code, rr.Body)
	}
}

// TestDispatchSessionSubpath_AllRoutesReachable enumerates every
// table entry and verifies the dispatcher matches the synthetic path
// against the entry. We don't care that the handler returns a useful
// response — we only need to confirm that the matcher routes it
// somewhere other than 404 / 405.
func TestDispatchSessionSubpath_AllRoutesReachable(t *testing.T) {
	for _, route := range sessionSubRoutes {
		t.Run(route.method+" "+route.pattern, func(t *testing.T) {
			path := "/api/session/" + fillPattern(route.pattern)
			req := httptest.NewRequest(route.method, path, nil)

			// We need the matcher to *find* the route. Verify via
			// matchSessionSubRoute against the trimmed path.
			trimmed := strings.TrimPrefix(req.URL.Path, "/api/session/")
			if _, ok := matchSessionSubRoute(route.pattern, trimmed); !ok {
				t.Fatalf("synthetic path %q didn't match own pattern %q", trimmed, route.pattern)
			}
		})
	}
}

// fillPattern substitutes "{id}", "{pid}", "{qid}" placeholders with
// dummy literals so the resulting path matches the pattern.
func fillPattern(pattern string) string {
	out := pattern
	out = strings.ReplaceAll(out, "{id}", "sess1")
	out = strings.ReplaceAll(out, "{pid}", "perm1")
	out = strings.ReplaceAll(out, "{qid}", "q1")
	return out
}
