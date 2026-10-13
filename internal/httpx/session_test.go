package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionPath(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/wrong", 404}, {"/api/session/", 400}, {"/api/session/bad%20id/queue", 400},
		{"/api/session/" + strings.Repeat("a", 257), 400}, {"/api/session/s_1-a/queue/q/move?platform=%20r-owner:claudecode%20", 200},
	} {
		rec := httptest.NewRecorder()
		WithSessionPath(rec, httptest.NewRequest(http.MethodGet, tc.path, nil), func(_ http.ResponseWriter, r *http.Request, id, rest string) {
			if id != "s_1-a" || rest != "queue/q/move" || PlatformHint(r) != "r-owner:claudecode" {
				t.Errorf("parsed %s %s %s", id, rest, PlatformHint(r))
			}
		})
		if rec.Code != tc.status {
			t.Errorf("%s: %d", tc.path, rec.Code)
		}
	}
}
