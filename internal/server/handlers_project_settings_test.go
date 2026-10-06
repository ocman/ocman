package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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

	expect(do(http.MethodGet, "/src/foo", ""), 200, `{"models":[],"off":false,"defaultAgent":"build"}`)
	if got := srv.projectDefaultModel(t.Context(), "/src/foo"); got != "" {
		t.Fatalf("unconfigured default = %q", got)
	}

	expect(do(http.MethodPost, "", `{"directory":"/src/.worktrees/foo/wt","models":["a/b","c/d"],"off":true}`), 200, "")
	expect(do(http.MethodGet, "/src/foo", ""), 200, `{"models":["a/b","c/d"],"off":true,"defaultAgent":"build"}`)
	// The save must drop the cached empty list for the whole project.
	if got := srv.projectDefaultModel(t.Context(), "/src/.worktrees/foo/x"); got != "a/b" {
		t.Fatalf("default after save = %q, want a/b", got)
	}
	if _, off := srv.projectModelList(t.Context(), "/src/foo"); !off {
		t.Fatal("off switch not surfaced to model selection")
	}

	expect(do(http.MethodPost, "", `{"directory":"/src/foo","models":["a/b","a/b"]}`), 400, "")
	expect(do(http.MethodGet, "/src/.worktrees/foo/other", ""), 200, `{"models":["a/b","c/d"],"off":true,"defaultAgent":"build"}`)

	expect(do(http.MethodPost, "", `{"directory":"/src/foo","models":[]}`), 200, "")
	if _, ok, _ := srv.stateDB.GetSetting(t.Context(), "project:/src/foo"); ok {
		t.Fatal("empty list left a row behind")
	}
	if got := srv.projectDefaultModel(t.Context(), "/src/foo"); got != "" {
		t.Fatalf("default after clearing = %q", got)
	}

	expect(do(http.MethodGet, "", ""), 400, "")
	expect(do(http.MethodPost, "", `{"models":["a/b"]}`), 400, "")
	expect(do(http.MethodDelete, "/src/foo", ""), 405, "")
}

func TestHandleModelFallthroughSettings(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	do := func(method, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleModelFallthroughSettings(rr, httptest.NewRequest(method, "/api/settings/model-fallthrough", strings.NewReader(body)))
		return rr
	}
	for _, tc := range []struct {
		method, body string
		code         int
		want         string
	}{
		{http.MethodGet, "", 200, `{"patienceMinutes":5,"fallbackMinutes":15}`},
		{http.MethodPost, `{"patienceMinutes":2,"fallbackMinutes":30}`, 200, ""},
		{http.MethodGet, "", 200, `{"patienceMinutes":2,"fallbackMinutes":30}`},
		{http.MethodPost, `{"patienceMinutes":0,"fallbackMinutes":30}`, 400, ""},
		{http.MethodPost, `{"patienceMinutes":2,"fallbackMinutes":99999}`, 400, ""},
		{http.MethodGet, "", 200, `{"patienceMinutes":2,"fallbackMinutes":30}`},
		{http.MethodDelete, "", 405, ""},
	} {
		rr := do(tc.method, tc.body)
		if rr.Code != tc.code || (tc.want != "" && strings.TrimSpace(rr.Body.String()) != tc.want) {
			t.Fatalf("%s %s: got %d %q; want %d %q", tc.method, tc.body, rr.Code, rr.Body.String(), tc.code, tc.want)
		}
	}
	// Read at decision time: the saved values reach the cooldown hook.
	if p, f := srv.cooldownTimes(t.Context()); p != 2*time.Minute || f != 30*time.Minute {
		t.Fatalf("cooldownTimes = %v, %v", p, f)
	}
	if err := srv.stateDB.SetSetting(t.Context(), modelFallthroughSettingKey, "{bad"); err != nil {
		t.Fatal(err)
	}
	if p, f := srv.cooldownTimes(t.Context()); p != 5*time.Minute || f != 15*time.Minute {
		t.Fatalf("corrupt setting: cooldownTimes = %v, %v, want defaults", p, f)
	}
	rr := httptest.NewRecorder()
	(&Server{}).handleModelFallthroughSettings(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil state db = %d", rr.Code)
	}
}
