package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHandleProjectSettings(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	do := func(method, dir, body string) *httptest.ResponseRecorder {
		target := "/api/project/settings"
		if dir != "" {
			target += "?dir=" + url.QueryEscape(dir)
		}
		rr := httptest.NewRecorder()
		srv.handleProjectSettings(rr, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rr
	}
	expect := func(rr *httptest.ResponseRecorder, code int, body string) {
		t.Helper()
		if rr.Code != code || (body != "" && strings.TrimSpace(rr.Body.String()) != body) {
			t.Fatalf("got %d %q; want %d %q", rr.Code, rr.Body.String(), code, body)
		}
	}

	expect(do(http.MethodGet, "/src/foo", ""), 200, `{"models":[],"off":false}`)

	expect(do(http.MethodPost, "", `{"directory":"/src/.worktrees/foo/wt","models":["a/b","c/d"],"off":true}`), 200, "")
	expect(do(http.MethodGet, "/src/foo", ""), 200, `{"models":["a/b","c/d"],"off":true}`)

	expect(do(http.MethodPost, "", `{"directory":"/src/foo","models":["a/b","a/b"]}`), 400, "")
	expect(do(http.MethodGet, "/src/.worktrees/foo/other", ""), 200, `{"models":["a/b","c/d"],"off":true}`)

	expect(do(http.MethodPost, "", `{"directory":"/src/foo","models":[]}`), 200, "")
	if _, ok, _ := srv.stateDB.GetSetting(t.Context(), "project:/src/foo"); ok {
		t.Fatal("empty list left a row behind")
	}

	expect(do(http.MethodGet, "", ""), 400, "")
	expect(do(http.MethodPost, "", `{"models":["a/b"]}`), 400, "")
	expect(do(http.MethodDelete, "/src/foo", ""), 405, "")
}
