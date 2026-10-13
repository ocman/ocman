package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type idleProvider struct{ platforms.Platform }

func (idleProvider) ID() platforms.ID                                      { return "idle-provider" }
func (idleProvider) ManagedIdleProjects(context.Context) ([]string, error) { return nil, nil }
func (idleProvider) CullIdleProject(context.Context, string, []string, func(context.Context) (bool, error)) (bool, error) {
	return false, nil
}

func TestIdleProjectsWiringAndGuards(t *testing.T) {
	srv, registry := newSessionsTestServer(t)
	registry.Register(idleProvider{})
	targets := srv.idleProjectTargets()
	if len(targets) != 1 || targets[0].Platform.ID() != "idle-provider" {
		t.Fatal(targets)
	}
	first := srv.idleProjectsModule()
	if first != srv.idleProjectsModule() {
		t.Fatal("rebuilt stateful module")
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		peer, method, body string
		code               int
	}{
		{"127.0.0.1:1234", "GET", "", 200},
		{"127.0.0.1:1234", "POST", `{"minutes":30}`, 200},
		{"10.0.0.5:1234", "POST", `{"minutes":1}`, 403},
		{"127.0.0.1:1234", "DELETE", "", 405},
	} {
		req := httptest.NewRequest(tc.method, "/api/settings/idle-project-timeout", strings.NewReader(tc.body))
		req.RemoteAddr = tc.peer
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.peer, tc.method, rr.Code, rr.Body.String())
		}
	}
	value, _, err := srv.stateDB.GetSetting(t.Context(), "projects.idle_timeout_minutes")
	if err != nil || value != "30" {
		t.Fatal(value, err)
	}
	if New(nil, nil, "", nil, nil).idleProjectsModule() != nil {
		t.Fatal("module created without state")
	}
	if targets := (&Server{}).idleProjectTargets(); len(targets) != 0 {
		t.Fatal(targets)
	}
}
