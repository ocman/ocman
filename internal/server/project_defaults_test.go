package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestProjectDefaults(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	if err := srv.stateDB.SetProjectSettings(t.Context(), "/src/foo", state.ProjectSettings{Models: []string{"p/fallback"}, Off: true}); err != nil {
		t.Fatal(err)
	}
	post := func(body string) int {
		rr := httptest.NewRecorder()
		srv.handleProjectSettings(rr, httptest.NewRequest(http.MethodPost, "/api/project/settings", strings.NewReader(body)))
		return rr.Code
	}
	if code := post(`{"directory":"/src/.worktrees/foo/wt","remoteId":"machine","defaults":{"model":"p/start","agent":"plan","worktree":"current"}}`); code != 200 {
		t.Fatalf("save: %d", code)
	}
	rr := httptest.NewRecorder()
	srv.handleProjectSettings(rr, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/src/foo&remoteId=machine", nil))
	var got struct {
		state.ProjectSettings
		DefaultAgent string          `json:"defaultAgent"`
		Defaults     projectDefaults `json:"defaults"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 || got.DefaultAgent != "plan" || got.Defaults.Model != "p/start" || got.Defaults.Worktree != "current" || !got.Off || len(got.Models) != 1 || got.Models[0] != "p/fallback" {
		t.Fatalf("settings: %d %s", rr.Code, rr.Body.String())
	}
	for _, owner := range []string{"", "local", "other"} {
		defaults, err := srv.getProjectDefaults(t.Context(), "/src/foo", owner)
		if err != nil || defaults != nil {
			t.Fatalf("owner %q: %v %v", owner, defaults, err)
		}
	}
	for _, body := range []string{
		`{"directory":"/src/foo","defaults":{"model":"invalid"}}`,
		`{"directory":"/src/foo","defaults":{"worktree":"elsewhere"}}`,
		`{"directory":"/src/foo","defaults":{"agent":" plan"}}`,
		`{"directory":"/src/foo","defaults":{"agent":"bad\nagent"}}`,
		`{"directory":"/src/foo","defaults":{"agent":"` + strings.Repeat("a", 201) + `"}}`,
	} {
		if code := post(body); code != 400 {
			t.Fatalf("invalid defaults: %d", code)
		}
	}
	if code := post(`{"directory":"/src/foo","remoteId":"machine","defaults":{"model":"","agent":"","worktree":""}}`); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	rr = httptest.NewRecorder()
	srv.getProjectSettings(rr, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/src/foo&remoteId=machine", nil))
	if !strings.Contains(rr.Body.String(), `"defaultAgent":"build"`) {
		t.Fatal(rr.Body.String())
	}
	if err := srv.stateDB.SetSetting(t.Context(), projectDefaultsKey("/src/foo", "local"), "{broken"); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	srv.getProjectSettings(rr, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/src/foo", nil))
	if rr.Code != 500 {
		t.Fatalf("corrupt defaults: %d", rr.Code)
	}
}
