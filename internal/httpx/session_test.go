package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionID(t *testing.T) {
	for _, tc := range []struct {
		id     string
		status int
	}{
		{"", 400}, {"bad id", 400}, {"bad/id", 400}, {strings.Repeat("a", 257), 400}, {"s_1-a", 200},
	} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/session/x/queue?platform=%20r-owner:claudecode%20", nil)
		r.SetPathValue("id", tc.id)
		id, ok := SessionID(rec, r)
		if ok != (tc.status == 200) || rec.Code != tc.status {
			t.Errorf("%q: ok=%v status=%d", tc.id, ok, rec.Code)
		}
		if ok && (id != tc.id || PlatformHint(r) != "r-owner:claudecode") {
			t.Errorf("parsed %s %s", id, PlatformHint(r))
		}
	}
}
