package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryPlanningHost struct {
	hostsvc.Host
	ensured, restarted *hostsvc.EnsureProjectOpencodeResult
	restartCalls       int
}

func TestFactoryStrongModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []platforms.SessionModel
		want   string
	}{
		{"none", nil, ""},
		{"balanced only", []platforms.SessionModel{{Provider: "p", Model: "opus", IsAvailable: true}}, ""},
		{"unavailable", []platforms.SessionModel{{Provider: "p", Model: "fable"}}, ""},
		{"astra", []platforms.SessionModel{{Provider: "p", Model: "gpt-6-astra", IsAvailable: true}}, "p/gpt-6-astra"},
		{"display name", []platforms.SessionModel{{Provider: "p", Model: "strong", ModelName: "Fable", IsAvailable: true}}, "p/strong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := factoryStrongModel(tc.models); got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
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
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	return []platforms.PermissionRule{
		{Permission: "external_directory", Pattern: filepath.Join(home, ".agents", "skills", "agent-skill", "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(home, ".claude", "skills", "claude-skill", "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(link, "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(configHome, "opencode", "skills", "user-skill", "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(dataSkill, "**"), Action: "allow"},
		{Permission: "external_directory", Pattern: filepath.Join(home, "data", "opencode", "tool-output", "**"), Action: "allow"},
	}
}

func TestFactoryExternalDirectoryRulesRejectsRelativeHome(t *testing.T) {
	t.Setenv("HOME", "relative")
	t.Setenv("XDG_CONFIG_HOME", "")
	if rules := factoryExternalDirectoryRules(); len(rules) != 0 {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestFactoryExternalDirectoryRulesDataPaths(t *testing.T) {
	for _, dataHome := range []string{"", "relative", "custom", "bad*path"} {
		t.Run(dataHome, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "relative")
			value := dataHome
			if dataHome == "custom" || dataHome == "bad*path" {
				value = filepath.Join(home, dataHome)
			}
			t.Setenv("XDG_DATA_HOME", value)
			root := filepath.Join(home, ".local", "share")
			if filepath.IsAbs(value) {
				root = value
			}
			output := filepath.Join(root, "opencode", "tool-output")
			if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
				t.Fatal(err)
			}
			target, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, output); err != nil {
				t.Fatal(err)
			}
			rules := factoryExternalDirectoryRules()
			if dataHome == "bad*path" {
				if len(rules) != 0 {
					t.Fatalf("wildcard path granted access: %#v", rules)
				}
				return
			}
			for _, directory := range []string{output, target} {
				want := platforms.PermissionRule{Permission: "external_directory", Pattern: filepath.Join(directory, "**"), Action: "allow"}
				if !slices.Contains(rules, want) {
					t.Errorf("missing rule %#v in %#v", want, rules)
				}
			}
		})
	}
}

func TestFactoryPlanningLauncherRejectsUnsafeProjectPatterns(t *testing.T) {
	launcher := factoryPlanningLauncher{server: New(nil, nil, "", platforms.NewRegistry(), nil)}
	for _, project := range []string{"relative", "/other/../repo", "/other*", "/other[repo"} {
		if _, err := launcher.LaunchPlanningSession(t.Context(), factory.PlanningSessionRequest{Repository: "/repo", Projects: []string{"/repo", project}}); err == nil {
			t.Fatalf("accepted project path %q", project)
		}
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
	srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: endpoint, RepoRoot: req.ProjectDir}, nil
	}})

	req := factory.PlanningSessionRequest{EpicID: "epic-1", WorkID: "work-1", AttemptID: "fa_1", AgentToken: "fat_secret", Repository: "/repo", Projects: []string{"/repo", "/other"}, Title: "Plan: Ship"}
	got, err := (factoryPlanningLauncher{server: srv}).LaunchPlanningSession(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got != (factory.PlanningSession{Platform: "local-agent", ID: "session-1"}) || ensured != "/repo" {
		t.Fatalf("session = %#v, ensured = %q", got, ensured)
	}
	if err := (factoryPlanningLauncher{server: srv}).PromptPlanningSession(context.Background(), got, req); err != nil {
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
		{Permission: "external_directory", Pattern: filepath.Join("/other", "**"), Action: "allow"},
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
	if !strings.Contains(sent.Message, "load the grilling skill if available") || !strings.Contains(sent.Message, "Do not propose an issue graph until the user tells you to proceed") || !strings.Contains(sent.Message, "Do not link to /factory/epics/epic-1 before submitting the full plan") || !strings.Contains(sent.Message, "End only that post-submission recap with") || !strings.Contains(sent.Message, "if the to-tickets skill is available") || !strings.Contains(sent.Message, "either way, split the plan into tracer-bullet vertical slices") || !strings.Contains(sent.Message, "Factory's proposal approval replaces") || !strings.Contains(sent.Message, "multiple focused implementation Issues by default") || !strings.Contains(sent.Message, "explicit edges") || !strings.Contains(sent.Message, "Mermaid flowchart") || !strings.Contains(sent.Message, "Approve and start implementation materializes the Plan and begins implementation") {
		t.Fatalf("prompt does not explain inline plan review: %q", sent.Message)
	}
	if !strings.Contains(sent.Message, "/repo") || !strings.Contains(sent.Message, "/other") {
		t.Fatalf("prompt does not list admitted projects: %q", sent.Message)
	}
	if !strings.Contains(sent.Message, "use them only for research and inspection; do not write files") {
		t.Fatalf("prompt does not constrain shell use: %q", sent.Message)
	}
	if !strings.HasSuffix(sent.Message, "[Review and approve the plan](/factory/epics/epic-1)") {
		t.Fatalf("prompt does not end with approval link: %q", sent.Message)
	}
	req.ScopeExpansion = true
	if err := (factoryPlanningLauncher{server: srv}).PromptPlanningSession(context.Background(), got, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent.Message, "submit_scope_plan") || !strings.Contains(sent.Message, "Do not ask for another approval") || strings.Contains(sent.Message, "At approval") {
		t.Fatalf("scope expansion prompt = %q", sent.Message)
	}
}

func TestFactoryPlanningLauncherRejectsUnsafeProjectPattern(t *testing.T) {
	launcher := factoryPlanningLauncher{server: New(nil, nil, "", platforms.NewRegistry(), nil)}
	_, err := launcher.LaunchPlanningSession(t.Context(), factory.PlanningSessionRequest{Repository: "/repo", Projects: []string{"/repo", "/other*"}})
	if err == nil || !strings.Contains(err.Error(), "invalid admitted project path") {
		t.Fatalf("LaunchPlanningSession error = %v", err)
	}
}

func TestFactoryUnblockLauncherCreatesReadOnlyConversation(t *testing.T) {
	endpoint := connectedPlanningMCP(t)
	var sent platforms.SendMessageRequest
	var rules platforms.SetPermissionRulesRequest
	platform := &fakePlatform{id: "local-agent", caps: platforms.Capabilities{PermissionRules: true}}
	platform.createSessionFn = func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		return &platforms.CreateSessionResponse{ID: "unblock-1"}, nil
	}
	platform.setPermissionRulesFn = func(req platforms.SetPermissionRulesRequest) error { rules = req; return nil }
	platform.sendMessageFn = func(req platforms.SendMessageRequest) error { sent = req; return nil }
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	srv.factory = &fakeFactoryService{
		epics:  []factory.WorkEpic{{ID: "epic-1", Goal: "Ship", Status: "open", InitialProject: "/repo", Projects: []model.EpicProject{{Path: "/repo"}, {Path: "/other", Removable: true}}}},
		issues: []factory.Issue{{ID: "issue-1", EpicID: "epic-1", Kind: "implementation", Title: "Transport", Status: "closed", Outcome: "failed", OutcomeReason: "merged branch"}},
	}
	srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: endpoint, RepoRoot: req.ProjectDir}, nil
	}})

	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/factory/epics/epic-1/issues/issue-1/unblock", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"id":"unblock-1"`) || sent.SessionID != "unblock-1" {
		t.Fatalf("response = %d %s, sent = %#v", recorder.Code, recorder.Body.String(), sent)
	}
	if !strings.Contains(sent.Message, "Allow and Reject buttons") || !strings.Contains(sent.Message, "cannot run before approval") || !strings.Contains(sent.Message, "factory_unblock") || !strings.Contains(sent.Message, "merged branch") {
		t.Fatalf("prompt = %q", sent.Message)
	}
	want := platforms.PermissionRule{Permission: "mcp_factory_unblock", Pattern: "factory_unblock", Action: "ask"}
	projectRule := platforms.PermissionRule{Permission: "external_directory", Pattern: filepath.Join("/other", "**"), Action: "allow"}
	if !slices.Contains(rules.Rules, want) || !slices.Contains(rules.Rules, projectRule) || slices.Contains(rules.Rules, platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"}) {
		t.Fatalf("rules = %#v", rules.Rules)
	}
	if !strings.Contains(sent.Message, "/repo") || !strings.Contains(sent.Message, "/other") {
		t.Fatalf("prompt does not list admitted projects: %q", sent.Message)
	}
	var token string
	srv.factoryUnblockTokens.Range(func(key, _ any) bool { token, _ = key.(string); return false })
	if token == "" || !srv.consumeFactoryUnblock(token, "epic-1") || srv.consumeFactoryUnblock(token, "epic-1") {
		t.Fatal("unblock token was not scoped and single-use")
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
	srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
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
