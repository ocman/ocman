package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDefaultPlatform(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "claudecode"})
	call := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handleDefaultPlatform(rec, httptest.NewRequest(method, "/api/settings/default-platform", strings.NewReader(body)))
		return rec
	}
	// Claude-only: the unset default is the enabled platform, never a
	// disabled OpenCode (FR-1/2).
	if rec := call(http.MethodGet, ""); !strings.Contains(rec.Body.String(), `"platform":"claudecode"`) {
		t.Fatalf("Claude-only default = %s", rec.Body)
	}
	reg.Register(&fakePlatform{id: "opencode"})
	if rec := call(http.MethodGet, ""); !strings.Contains(rec.Body.String(), `"platform":"opencode"`) {
		t.Fatalf("default = %s", rec.Body)
	}
	for _, bad := range []string{`{"platform":"nope"}`, `{"platform":"r-x:claudecode"}`} {
		if rec := call(http.MethodPost, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", bad, rec.Code)
		}
	}
	if rec := call(http.MethodPost, `{"platform":"claudecode"}`); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodGet, ""); !strings.Contains(rec.Body.String(), `"platform":"claudecode"`) {
		t.Fatalf("saved default = %s", rec.Body)
	}
	if rec := call(http.MethodDelete, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete = %d", rec.Code)
	}
}
