package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesIncludesPlatformOwners(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	for _, id := range []string{"opencode", "claudecode", "r-fractal:opencode", "r-fractal:claudecode", "r-other:claudecode"} {
		reg.Register(&fakePlatform{PlatformID: id})
	}
	w := httptest.NewRecorder()
	srv.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var response struct {
		Platforms []capabilityEntry `json:"platforms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, p := range response.Platforms {
		owners[p.ID] = p.RemoteID
	}
	for id, want := range map[string]string{
		"opencode": "", "claudecode": "",
		"r-fractal:opencode": "fractal", "r-fractal:claudecode": "fractal", "r-other:claudecode": "other",
	} {
		if got, ok := owners[id]; !ok || got != want {
			t.Errorf("platform %s owner = %q (present %v), want %q", id, got, ok, want)
		}
	}
}
