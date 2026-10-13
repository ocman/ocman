package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type createPathHost struct {
	hostsvc.Host
	owner     string
	ensured   string
	worktree  hostsvc.WorktreeSessionRequest
	ensureErr error
}

type runtimeAdapter struct{ *fakePlatform }

func (*createPathHost) Capabilities() hostsvc.HostCaps { return hostsvc.HostCaps{} }

func (runtimeAdapter) EnsureRuntime(_ context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return &hostsvc.EnsureRuntimeResult{Endpoint: "unix:///tmp/fake.sock", RepoRoot: dir}, nil
}

func TestLocalHostUsesAdapterRuntime(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(runtimeAdapter{&fakePlatform{id: "fake-provider"}})
	host := srv.newLocalHost()
	result, err := host.EnsureRuntime(t.Context(), "fake-provider", "/repo")
	if err != nil || result.RepoRoot != "/repo" || result.Port() != "" {
		t.Fatalf("runtime = %+v, %v", result, err)
	}
	if _, err := host.EnsureRuntime(t.Context(), "opencode", "/repo"); err == nil {
		t.Fatal("disabled OpenCode accepted")
	}
}

func (h *createPathHost) RemoteID() string {
	if h.owner == "" {
		return "local"
	}
	return h.owner
}
func (h *createPathHost) EnsureRuntime(_ context.Context, platform, _ string) (*hostsvc.EnsureRuntimeResult, error) {
	h.ensured = platform
	if h.ensureErr != nil {
		return nil, h.ensureErr
	}
	return &hostsvc.EnsureRuntimeResult{Endpoint: "unix:///tmp/fake.sock"}, nil
}

func TestProviderEnsureFailureStopsCreate(t *testing.T) {
	for _, path := range []string{"start", "mcp"} {
		t.Run(path, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			host := &createPathHost{ensureErr: errors.New("provider launch failed")}
			srv.hostRouter = hostsvc.NewRouter(host)
			creates := 0
			reg.Register(&fakePlatform{id: "fake-provider", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
				creates++
				return &platforms.CreateSessionResponse{ID: "child"}, nil
			}})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"directory":"/repo","platform":"fake-provider"}`))
			switch path {
			case "start":
				srv.handleStartSession(w, r)
			case "mcp":
				_, err := (sessionMCPService{server: srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{Platform: "fake-provider", Directory: "/repo", Prompt: "hello"})
				if !errors.Is(err, host.ensureErr) {
					t.Errorf("MCP error = %v", err)
				}
			}
			if creates != 0 {
				t.Fatalf("created %d sessions after runtime launch failed", creates)
			}
			if path != "mcp" && w.Code == http.StatusOK {
				t.Fatal("launch failure returned success")
			}
		})
	}
}
func (h *createPathHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.worktree = req
	return &hostsvc.WorktreeSessionResult{SessionID: "child", WorktreePath: "/repo/worktree", Branch: req.Branch}, nil
}

func TestCreatePathsCarryPlatform(t *testing.T) {
	for _, path := range []string{"start", "create", "start-worktree", "wt", "mcp", "factory-plan", "factory-implementation"} {
		t.Run(path, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			host := &createPathHost{}
			srv.hostRouter = hostsvc.NewRouter(host)
			var created platforms.CreateSessionRequest
			reg.Register(&fakePlatform{id: "fake-provider", caps: platforms.Capabilities{PermissionRules: true},
				sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
				createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					created = req
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				},
				setPermissionRulesFn: func(platforms.SetPermissionRulesRequest) error { return nil },
			})
			if err := srv.stateDB.SetSetting(t.Context(), state.DefaultPlatformSetting, "fake-provider"); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"directory":"/repo","projectDir":"/repo","branch":"branch","platform":"fake-provider"}`))
			switch path {
			case "start":
				srv.handleStartSession(w, r)
			case "create":
				srv.handleCreateSession(w, r)
			case "start-worktree":
				r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"directory":"/repo","worktree":true,"platform":"fake-provider"}`))
				srv.handleStartSession(w, r)
			case "wt":
				r = httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(`{"projectDir":"/repo","branch":"branch","platform":"fake-provider"}`))
				srv.handleWorktreeCreateAndLaunch(w, r)
			case "mcp":
				result, err := (sessionMCPService{server: srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{Directory: "/repo", Prompt: "hello", Model: "fake-provider:anthropic/claude"})
				if err != nil || result.Platform != "fake-provider" {
					t.Fatalf("MCP = %+v, %v", result, err)
				}
			case "factory-plan":
				result, err := srv.factoryModule().PlanningLauncher().LaunchPlanningSession(t.Context(), factory.PlanningSessionRequest{Platform: "fake-provider", Repository: "/repo"})
				if err != nil || result.Platform != "fake-provider" {
					t.Fatalf("Factory plan = %+v, %v", result, err)
				}
			case "factory-implementation":
				srv.stateDB = nil // delivery-target validation is covered by the existing Factory suite
				result, err := srv.factoryModule().ImplementationLauncher().LaunchImplementationSession(t.Context(), factory.ImplementationSessionRequest{Platform: "fake-provider", Repository: "/repo", Profile: "factory-implement/v1"})
				if err != nil || result.Platform != "fake-provider" {
					t.Fatalf("Factory implementation = %+v, %v", result, err)
				}
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			worktree := strings.Contains(path, "worktree") || path == "wt" || path == "factory-implementation"
			if worktree {
				if host.worktree.Platform != "fake-provider" {
					t.Fatalf("worktree = %+v", host.worktree)
				}
			} else if host.ensured != "fake-provider" || created.Port != "" {
				t.Fatalf("runtime = %q, create = %+v", host.ensured, created)
			}
		})
	}
}

func TestCreateDefaultAndOwner(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "fake-provider", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		return &platforms.CreateSessionResponse{ID: "child"}, nil
	}, sendMessageFn: func(platforms.SendMessageRequest) error { return nil }})
	if err := srv.stateDB.SetSetting(t.Context(), state.DefaultPlatformSetting, "fake-provider"); err != nil {
		t.Fatal(err)
	}
	host := &createPathHost{owner: "box"}
	id, base, err := srv.createPlatform(t.Context(), host, "", "")
	if err != nil || id != "r-box:fake-provider" || base != "fake-provider" {
		t.Fatalf("default = %q %q, %v", id, base, err)
	}
	if _, _, err := srv.createPlatform(t.Context(), host, "r-other:fake-provider", ""); err == nil {
		t.Fatal("wrong owner accepted")
	}
	for _, body := range []string{`{"directory":"/repo"}`, `{"directory":"/repo","platform":"fake-provider","send":{"message":"hello","model":"openai/test"}}`} {
		srv.hostRouter = hostsvc.NewRouter(&createPathHost{})
		w := httptest.NewRecorder()
		srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["platform"] != "fake-provider" {
			t.Fatalf("default start = %s, %v", w.Body.String(), err)
		}
	}
	_ = srv.stateDB.Close()
	if _, _, err := srv.createPlatform(t.Context(), host, "", ""); err == nil {
		t.Fatal("default read error swallowed")
	}
}

// The first prompt's model picks the platform of a new conversation, and a
// model contradicting the named platform is refused before anything launches.
func TestStartPlatformFollowsModel(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	host := &createPathHost{}
	srv.hostRouter = hostsvc.NewRouter(host)
	var sent platforms.SendMessageRequest
	reg.Register(&fakePlatform{id: "fake-provider",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "child"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil },
	})
	w := httptest.NewRecorder()
	srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"directory":"/repo","send":{"message":"hi","model":"fake-provider:acme/m"}}`)))
	if !strings.Contains(w.Body.String(), `"platform":"fake-provider"`) || host.ensured != "fake-provider" || sent.Model != "acme/m" {
		t.Fatalf("start = %d %s, ensured %q, sent %+v", w.Code, w.Body.String(), host.ensured, sent)
	}
	host.ensured = ""
	w = httptest.NewRecorder()
	srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"directory":"/repo","platform":"opencode","send":{"message":"hi","model":"fake-provider:acme/m"}}`)))
	if w.Code != http.StatusBadRequest || host.ensured != "" {
		t.Fatalf("mismatch = %d %s, ensured %q", w.Code, w.Body.String(), host.ensured)
	}
}
