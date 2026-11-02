package moduletest

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// Do serves one request through h and returns the recorded response. A
// non-empty body is sent as JSON; a non-empty peer replaces the request's
// RemoteAddr, which otherwise is httptest's non-loopback default.
func Do(h http.Handler, method, target, body, peer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if peer != "" {
		req.RemoteAddr = peer
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
