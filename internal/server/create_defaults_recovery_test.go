package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestRESTCreateResolvesDefaultBeforeRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, saved, explicit, want string
		enabled                     []string
	}{
		{"OpenCode only", "claudecode", "", "opencode", []string{"opencode"}},
		{"Claude only", "opencode", "", "claudecode", []string{"claudecode"}},
		{"mixed saved Claude", "claudecode", "", "claudecode", []string{"opencode", "claudecode"}},
		{"mixed saved OpenCode", "opencode", "", "opencode", []string{"opencode", "claudecode"}},
		{"explicit overrides saved", "claudecode", "opencode", "opencode", []string{"opencode", "claudecode"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			host := &createPathHost{}
			srv.hostRouter = hostsvc.NewRouter(host)
			created := ""
			for _, id := range tc.enabled {
				reg.Register(&fakePlatform{id: id, createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					created = id
					if req.Directory != "/repo" {
						t.Fatalf("directory = %q", req.Directory)
					}
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				}})
			}
			if err := srv.stateDB.SetSetting(t.Context(), state.DefaultPlatformSetting, tc.saved); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			srv.handleCreateSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"directory":"/repo","platform":"`+tc.explicit+`"}`)))
			if w.Code != http.StatusOK || host.ensured != tc.want || created != tc.want {
				t.Fatalf("status %d, ensured %q, created %q: %s", w.Code, host.ensured, created, w.Body.String())
			}
		})
	}
}

func TestRESTCreateAuthoritativeOwner(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"remote default", `{"remoteId":"box"}`, "r-box:claudecode", 200},
		{"compound platform", `{"platform":"r-box:claudecode"}`, "r-box:claudecode", 200},
		{"local explicit", `{"remoteId":"local","platform":"claudecode"}`, "claudecode", 200},
		{"owner mismatch", `{"remoteId":"box","platform":"claudecode"}`, "", 400},
		{"disconnected owner", `{"remoteId":"gone"}`, "", 503},
		{"disconnected compound", `{"platform":"r-gone:claudecode"}`, "", 503},
		{"unknown platform", `{"platform":"unknown"}`, "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			local, box := &createPathHost{}, &createPathHost{owner: "box"}
			srv.hostRouter = hostsvc.NewRouter(local)
			srv.hostRouter.RegisterRemote("box", box)
			created := ""
			for _, id := range []string{"claudecode", "r-box:claudecode"} {
				reg.Register(&fakePlatform{id: id, createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					created = id
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				}})
			}
			w := httptest.NewRecorder()
			body := `{"directory":"/repo",` + strings.TrimPrefix(tc.body, "{")
			srv.handleCreateSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body)))
			if w.Code != tc.status || created != tc.want {
				t.Fatalf("status %d, created %q: %s", w.Code, created, w.Body.String())
			}
			if tc.want == "" && (local.ensured != "" || box.ensured != "") {
				t.Fatal("rejected request launched a runtime")
			}
			if tc.want == "r-box:claudecode" && (box.ensured != "claudecode" || local.ensured != "") {
				t.Fatalf("runtime owner: local %q, box %q", local.ensured, box.ensured)
			}
		})
	}
}
