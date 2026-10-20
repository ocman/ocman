package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

func TestRequestProjectionOnlyTouchesDeclaredRouteFields(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		translate          bool
	}{
		{"state mutation", "POST", "/api/session/seen", true},
		{"routine create", "POST", "/api/routines", true},
		{"routine update", "PUT", "/api/routines/routine", true},
		{"routine create slash", "POST", "/api/routines/", true},
		{"routine update slash", "PUT", "/api/routines/routine/", true},
		{"unknown route", "POST", "/api/unrelated", false},
		{"wrong method", "GET", "/api/routines", false},
		{"message body", "POST", "/api/session/native/message", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, ownedID := identityHTTPFixture(t)
			w := httptest.NewRecorder()
			s.withSessionViews(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Platform        string            `json:"platform"`
					SessionID       string            `json:"sessionId"`
					ParentSessionID string            `json:"parentSessionId"`
					Metadata        map[string]string `json:"metadata"`
				}
				if !httpx.ReadJSON(w, r, 4096, &req) {
					return
				}
				want := ownedID
				if tc.translate {
					want = "native"
				}
				if req.SessionID != want || req.ParentSessionID != ownedID || req.Metadata["sessionId"] != ownedID {
					t.Fatalf("undeclared references changed: %+v", req)
				}
				w.WriteHeader(http.StatusNoContent)
			})(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"platform":"test","sessionId":"`+ownedID+`","parentSessionId":"`+ownedID+`","metadata":{"sessionId":"`+ownedID+`"}}`)))
			if w.Code != http.StatusNoContent {
				t.Fatalf("projection failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
