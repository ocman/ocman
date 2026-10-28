package factorymod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// localMCPURL is the URL the fake OpenCode endpoints report as configured.
func localMCPURL() string { return "http://localhost:8229/mcp" }

type modelPlatform struct {
	platforms.Platform
	models []platforms.SessionModel
}

func (p modelPlatform) SessionModels(context.Context, string) (*platforms.SessionModelsResponse, error) {
	return &platforms.SessionModelsResponse{Models: p.models}, nil
}

type stubRegistry map[platforms.ID]platforms.Platform

func (r stubRegistry) Get(id platforms.ID) (platforms.Platform, bool) { p, ok := r[id]; return p, ok }
func (r stubRegistry) Platforms() []platforms.Platform                { return nil }

func TestFactoryVerificationModel(t *testing.T) {
	registry := stubRegistry{"opencode": modelPlatform{models: []platforms.SessionModel{{Provider: "p", Model: "fable-1", IsAvailable: true}}}}
	launcher := NewModule(Deps{Service: struct{ Service }{}, Registry: registry}).ImplementationLauncher()
	session := factory.PlanningSession{Platform: "opencode", ID: "s"}
	if got := launcher.verificationModel(t.Context(), session, "x/frozen"); got != "x/frozen" {
		t.Fatalf("explicit model = %q", got)
	}
	if got := launcher.verificationModel(t.Context(), session, ""); got != "p/fable-1" {
		t.Fatalf("default validator model = %q", got)
	}
	if got := launcher.verificationModel(t.Context(), factory.PlanningSession{Platform: "missing"}, ""); got != "" {
		t.Fatalf("missing platform model = %q", got)
	}
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
	launcher := NewModule(Deps{Service: struct{ Service }{}, MCPURL: localMCPURL, OpenCodeAuth: func() ocapi.Auth { return ocapi.New("secret") }}).PlanningLauncher()
	if err := launcher.ensureMCP(t.Context(), &hostsvc.EnsureRuntimeResult{Endpoint: endpoint.URL}); err != nil {
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
	launcher := NewModule(Deps{Service: struct{ Service }{}, MCPURL: localMCPURL, OpenCodeAuth: func() ocapi.Auth { return ocapi.Auth{} }}).PlanningLauncher()
	if err := launcher.ensureMCP(t.Context(), &hostsvc.EnsureRuntimeResult{Endpoint: endpoint.URL}); err != nil || !connected {
		t.Fatalf("ensureMCP = %v, connected = %v", err, connected)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(broken.Close)
	if err := launcher.ensureMCP(t.Context(), &hostsvc.EnsureRuntimeResult{Endpoint: broken.URL}); err == nil || !strings.Contains(err.Error(), "check planning MCP config") {
		t.Fatalf("ensureMCP error = %v", err)
	}
}

func TestFactoryVerificationPrompt(t *testing.T) {
	prompt := factoryVerificationPrompt(factory.ImplementationSessionRequest{WorkID: "w1", EpicID: "e1", Branch: "factory/e1", TargetBranch: "main", AttemptID: "a1", AgentToken: "tok", Criteria: "### i1 — API\n- [ ] returns 200"}, "Formula body")
	for _, want := range []string{"Formula body", "- [ ] returns 200", "read-only validator", "PASS or FAIL", "a1", "tok", "checks_running", "skipped tests"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestFactoryMCPRules(t *testing.T) {
	rule := platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"}
	for platform, want := range map[string]int{"opencode": 1, "r-box:opencode": 1, "claudecode": 2, "r-box:claudecode": 2} {
		if got := factoryMCPRules(platform, rule); len(got) != want || got[0] != rule {
			t.Errorf("%s = %+v", platform, got)
		}
	}
}
