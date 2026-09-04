package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryPlanningHost struct {
	hostsvc.Host
	ensured, restarted *hostsvc.EnsureProjectOpencodeResult
	restartCalls       int
}

func (h *factoryPlanningHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return h.ensured, nil
}

func (h *factoryPlanningHost) RestartProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.restartCalls++
	return h.restarted, nil
}

func connectedPlanningMCP(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			_, _ = w.Write([]byte(`{"mcp":{"ocman":{"url":"http://localhost:8229/mcp"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ocman":{"status":"connected"}}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func factorySkillRulesFixture(t *testing.T) []platforms.PermissionRule {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configHome := filepath.Join(home, "config")
	dataSkill := filepath.Join(home, "data", "ocman", "opencode", "skills", "ocman-factory")
	directories := []string{
		filepath.Join(configHome, "opencode", "skills", "user-skill"),
		filepath.Join(configHome, "opencode", "skills", "bad[skill"),
		filepath.Join(home, ".claude", "skills", "claude-skill"),
		filepath.Join(home, ".agents", "skills", "agent-skill"),
		dataSkill,
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("# Skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(configHome, "opencode", "skills", "ocman-factory")
	if err := os.Symlink(dataSkill, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	return []platforms.PermissionRule{
		{Permission: "external_directory", Pattern: filepath.Join(home, ".agents", "skills", "agent-skill", "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(home, ".claude", "skills", "claude-skill", "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(configHome, "opencode", "skills", "user-skill", "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(dataSkill, "**"), Action: "allow"},
	}
}

func TestFactorySkillDirectoryRulesRejectsRelativeHome(t *testing.T) {
	t.Setenv("HOME", "relative")
	t.Setenv("XDG_CONFIG_HOME", "")
	if rules := factorySkillDirectoryRules(); len(rules) != 0 {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestFactoryPlanningLauncherUsesLocalHostAndAppliesBoundedRules(t *testing.T) {
	skillRules := factorySkillRulesFixture(t)
	endpoint := connectedPlanningMCP(t)
	var ensured string
	var created platforms.CreateSessionRequest
	var sent platforms.SendMessageRequest
	var rules platforms.SetPermissionRulesRequest
	platform := &fakePlatform{id: "local-agent", caps: platforms.Capabilities{PermissionRules: true}}
	platform.createSessionFn = func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		created = req
		return &platforms.CreateSessionResponse{ID: "session-1"}, nil
	}
	platform.setPermissionRulesFn = func(req platforms.SetPermissionRulesRequest) error {
		rules = req
		return nil
	}
	platform.sendMessageFn = func(req platforms.SendMessageRequest) error { sent = req; return nil }
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	srv.hostRouter = hostsvc.NewRouter(&promptEnsureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: endpoint, RepoRoot: req.ProjectDir}, nil
	}})

	got, err := (factoryPlanningLauncher{server: srv}).LaunchPlanningSession(context.Background(), factory.PlanningSessionRequest{EpicID: "epic-1", WorkID: "work-1", Repository: "/repo", Title: "Plan: Ship"})
	if err != nil {
		t.Fatal(err)
	}
	if got != (factory.PlanningSession{Platform: "local-agent", ID: "session-1"}) || ensured != "/repo" {
		t.Fatalf("session = %#v, ensured = %q", got, ensured)
	}
	if err := (factoryPlanningLauncher{server: srv}).PromptPlanningSession(context.Background(), got, factory.PlanningSessionRequest{EpicID: "epic-1", WorkID: "work-1", AttemptID: "fa_1", AgentToken: "fat_secret"}); err != nil {
		t.Fatal(err)
	}
	if created != (platforms.CreateSessionRequest{Directory: "/repo", Title: "Plan: Ship", Port: strings.TrimPrefix(endpoint, "http://127.0.0.1:")}) {
		t.Fatalf("create = %#v", created)
	}
	wantRules := []platforms.PermissionRule{
		{Permission: "read", Pattern: "*", Action: "allow"},
		{Permission: "glob", Pattern: "*", Action: "allow"},
		{Permission: "grep", Pattern: "*", Action: "allow"},
		{Permission: "list", Pattern: "*", Action: "allow"},
		{Permission: "external_directory", Pattern: "*", Action: "deny"},
	}
	wantRules = append(wantRules, skillRules...)
	wantRules = append(wantRules, platforms.PermissionRule{Permission: "bash", Pattern: "*", Action: "deny"}, platforms.PermissionRule{Permission: "edit", Pattern: "*", Action: "deny"}, platforms.PermissionRule{Permission: "task", Pattern: "*", Action: "deny"}, platforms.PermissionRule{Permission: "webfetch", Pattern: "*", Action: "deny"}, platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"})
	if rules.SessionID != "session-1" || !reflect.DeepEqual(rules.Rules, wantRules) {
		t.Fatalf("permission rules = %#v", rules)
	}
	// The agent can only submit with its attempt token, so the prompt must carry it.
	if sent.SessionID != "session-1" || !strings.Contains(sent.Message, "epic-1") || !strings.Contains(sent.Message, "work-1") || !strings.Contains(sent.Message, "submit_proposal") || !strings.Contains(sent.Message, "attempt_id fa_1") || !strings.Contains(sent.Message, "attempt_token fat_secret") {
		t.Fatalf("prompt = %#v", sent)
	}
}

func TestFactoryPlanningLauncherReturnsRestrictedSessionWhenCleanupFails(t *testing.T) {
	endpoint := connectedPlanningMCP(t)
	platform := &fakePlatform{id: "local-agent", caps: platforms.Capabilities{PermissionRules: true}}
	platform.createSessionFn = func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		return &platforms.CreateSessionResponse{ID: "restricted-session"}, nil
	}
	platform.setPermissionRulesFn = func(platforms.SetPermissionRulesRequest) error { return errors.New("rules failed") }
	platform.disposeFn = func(platforms.DisposeSessionRequest) error { return errors.New("dispose failed") }
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	srv.hostRouter = hostsvc.NewRouter(&promptEnsureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: endpoint, RepoRoot: req.ProjectDir}, nil
	}})

	got, err := (factoryPlanningLauncher{server: srv}).LaunchPlanningSession(context.Background(), factory.PlanningSessionRequest{Repository: "/repo", Title: "Plan: Ship"})
	if err == nil || got != (factory.PlanningSession{Platform: "local-agent", ID: "restricted-session"}) {
		t.Fatalf("LaunchPlanningSession = %#v, %v", got, err)
	}
}

func TestFactoryPlanningLauncherRejectsStaleMCPConfigWithoutRestart(t *testing.T) {
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			_, _ = w.Write([]byte(`{"mcp":{"ocman":{"url":"http://localhost:8228/mcp"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ocman":{"status":"failed","error":"403"}}`))
	}))
	t.Cleanup(stale.Close)
	host := &factoryPlanningHost{
		ensured: &hostsvc.EnsureProjectOpencodeResult{Endpoint: stale.URL, RepoRoot: "/repo"},
	}
	var created platforms.CreateSessionRequest
	platform := &fakePlatform{id: "local-agent", caps: platforms.Capabilities{PermissionRules: true}}
	platform.createSessionFn = func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		created = req
		return &platforms.CreateSessionResponse{ID: "session-1"}, nil
	}
	platform.setPermissionRulesFn = func(platforms.SetPermissionRulesRequest) error { return nil }
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	srv.mcpAddr = "127.0.0.1:8227"
	srv.hostRouter = hostsvc.NewRouter(host)

	if _, err := (factoryPlanningLauncher{server: srv}).LaunchPlanningSession(t.Context(), factory.PlanningSessionRequest{Repository: "/repo"}); err == nil || !strings.Contains(err.Error(), "restart OpenCode after configuring") {
		t.Fatalf("LaunchPlanningSession error = %v", err)
	}
	if host.restartCalls != 0 || created.Port != "" {
		t.Fatalf("restart calls = %d, create = %#v", host.restartCalls, created)
	}
}

func TestFactoryPlanningMCPUsesOpenCodeAuth(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/config" {
			_, _ = w.Write([]byte(`{"mcp":{"ocman":{"url":"http://localhost:8229/mcp"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ocman":{"status":"connected"}}`))
	}))
	t.Cleanup(endpoint.Close)
	srv := New(nil, nil, "", platforms.NewRegistry(), nil).WithOpenCodeAuth(ocapi.New("secret"))
	if err := (factoryPlanningLauncher{server: srv}).ensureMCP(t.Context(), &hostsvc.EnsureProjectOpencodeResult{Endpoint: endpoint.URL}); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryPlanningMCPConnectsAndReportsUpstreamFailures(t *testing.T) {
	connected := false
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config":
			_, _ = w.Write([]byte(`{"mcp":{"ocman":{"url":"http://localhost:8229/mcp"}}}`))
		case "/mcp":
			status := "disconnected"
			if connected {
				status = "connected"
			}
			_, _ = w.Write([]byte(`{"ocman":{"status":"` + status + `"}}`))
		case "/mcp/ocman/connect":
			connected = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(endpoint.Close)
	launcher := factoryPlanningLauncher{server: New(nil, nil, "", platforms.NewRegistry(), nil)}
	if err := launcher.ensureMCP(t.Context(), &hostsvc.EnsureProjectOpencodeResult{Endpoint: endpoint.URL}); err != nil || !connected {
		t.Fatalf("ensureMCP = %v, connected = %v", err, connected)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(broken.Close)
	if err := launcher.ensureMCP(t.Context(), &hostsvc.EnsureProjectOpencodeResult{Endpoint: broken.URL}); err == nil || !strings.Contains(err.Error(), "check planning MCP config") {
		t.Fatalf("ensureMCP error = %v", err)
	}
}
