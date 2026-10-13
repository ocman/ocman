package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
)

// Claude-only and mixed-platform user workflows (FR-1/2/4, AD-1/13).

// A create without a platform follows the default, and one made from a
// calling session follows that session's platform, not the default.
func TestClaudeMCPCreateDefaultsAndCallingSession(t *testing.T) {
	srv, adapter, _ := newClaudeServer(t)
	repo := claudeRepo(t)
	svc := sessionMCPService{server: srv}
	first, err := svc.CreateSession(t.Context(), internalmcp.CreateSessionRequest{Directory: repo, Prompt: "Reply with exactly SPIKE_OK."})
	if err != nil || first.Platform != "claudecode" {
		t.Fatalf("Claude-only default create = %+v, %v", first, err)
	}
	detail, err := adapter.Session(t.Context(), first.SessionID, 1, 0)
	if err != nil || !detail.Session.LiveConnection {
		t.Fatalf("launched session = %+v, %v; want a live connection", detail, err)
	}
	// With OpenCode enabled the default moves to it, but a child of the
	// Claude session stays on Claude.
	srv.registry.Register(&fakePlatform{id: "opencode"})
	child, err := svc.CreateSession(t.Context(), internalmcp.CreateSessionRequest{SessionID: first.SessionID, Directory: repo, Prompt: "again"})
	if err != nil || child.Platform != "claudecode" {
		t.Fatalf("calling-session create = %+v, %v", child, err)
	}
}

// readOnlyPlatform reports every session read-only, like a Claude session
// started in a terminal.
type readOnlyPlatform struct{ *fakePlatform }

func (readOnlyPlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return &platforms.SessionLifecycle{Status: db.StatusDone, ReadOnly: true}, nil
}

func TestQueueRefusesReadOnlySession(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(readOnlyPlatform{&fakePlatform{id: "claudecode"}})
	err := srv.enqueueFollowUp(t.Context(), "claudecode", platforms.SendMessageRequest{SessionID: "terminal", Message: "hi"})
	var upstream *platforms.UpstreamError
	if !errors.As(err, &upstream) || upstream.Status != http.StatusForbidden {
		t.Fatalf("enqueue = %v, want refused", err)
	}
	if held, _ := srv.queueSvc().List(t.Context(), "claudecode", "terminal"); len(held) != 0 {
		t.Fatalf("held = %+v", held)
	}
}

// The projects index lists directories that only Claude sessions use, with
// their totals; subagents and sessions without a directory add nothing.
func TestProjectsIndexMergesPlatformDirectories(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "claudecode", sessions: []db.Session{
		{ID: "a", Directory: "/work/repo", TimeUpdated: 10, MessageCount: 2, TotalInputTokens: 5, TotalOutputTokens: 3, TotalCost: 0.5},
		{ID: "b", Directory: "/work/repo", TimeUpdated: 30, MessageCount: 1},
		{ID: "c", Directory: "/work/repo", ParentID: "a", MessageCount: 9},
		{ID: "d"},
	}})
	reg.Register(&fakePlatform{id: "broken", sessionsErr: errors.New("down")})
	reg.Register(&fakePlatform{id: "r-box:claudecode", sessions: []db.Session{{ID: "r", Directory: "/remote"}}})
	srv.projects.enrich = func(context.Context, []db.ProjectStats) error { return nil }
	if err := srv.refreshProjectsIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	projects, _ := srv.projectsSnapshot()
	if len(projects) != 1 {
		t.Fatalf("projects = %+v", projects)
	}
	if p := projects[0]; p.Directory != "/work/repo" || p.SessionCount != 2 || p.MessageCount != 3 || p.LastUsed != 30 ||
		p.TotalTokensIn != 5 || p.TotalTokensOut != 3 || p.TotalCost != 0.5 {
		t.Fatalf("project = %+v", p)
	}
	if caps := srv.hostCaps(); !caps.Projects || caps.OpenCode {
		t.Fatalf("host caps = %+v", caps)
	}
}

// The plugin catalog follows the installation's platform, and never
// ensures OpenCode when it is disabled.
func TestPluginProjectCatalogUsesInstallationPlatform(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	host := &createPathHost{}
	srv.hostRouter = hostsvc.NewRouter(host)
	reg.Register(&fakePlatform{id: "claudecode", directoryCatalogFn: func(req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		if req.Directory != "/repo" {
			t.Fatalf("catalog directory = %q", req.Directory)
		}
		return &platforms.DirectoryCatalog{Agents: []platforms.AgentCatalogEntry{{Name: "general"}},
			Models: &platforms.SessionModelsResponse{Models: []platforms.SessionModel{{Provider: "anthropic", Model: "haiku"}}}}, nil
	}})
	input := func(platform string) remote.PluginInput {
		values := map[string]json.RawMessage{"directory": json.RawMessage(`"/repo"`)}
		if platform != "" {
			values["platform"], _ = json.Marshal(platform)
		}
		return remote.PluginInput{Values: values}
	}
	catalog := func(platform string) remote.PluginResponse {
		return srv.pluginsModule().RemoteOperation(t.Context(), remote.PluginRequest{Operation: "project-catalog", PluginID: "org.ocman.slack", Input: input(platform)})
	}
	for _, platform := range []string{"", "claudecode"} {
		if got := catalog(platform); got.Error != nil || string(got.Value) != `{"agents":["general"],"models":["anthropic/haiku"]}` {
			t.Fatalf("catalog(%q) = %s, %v", platform, got.Value, got.Error)
		}
	}
	if got := catalog("opencode"); got.Error == nil || got.Error.Category != plugins.ErrorInvalidArgument {
		t.Fatalf("disabled OpenCode catalog = %+v", got)
	}
	if host.ensured != "" {
		t.Fatalf("ensured %q", host.ensured)
	}
}

// Without OpenCode, its settings answer without discovering an instance.
func TestOpenCodeSettingsWithoutOpenCode(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "claudecode"})
	if reason := srv.mcpUnavailableReason(); reason == "" {
		t.Fatal("MCP config offered without OpenCode")
	}
	rec := httptest.NewRecorder()
	srv.handleMCPConfigInstall(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/config/install", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("install = %d", rec.Code)
	}
	if srv.aaSvc() == nil || srv.openCodeEnabled() {
		t.Fatal("OpenCode reported enabled")
	}
}

// A remote target's default is one of the platforms that remote runs.
func TestCreatePlatformDefaultFollowsRemote(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "opencode"})
	reg.Register(&fakePlatform{id: "r-box:claudecode"})
	id, base, err := srv.createPlatform(t.Context(), &createPathHost{owner: "box"}, "", "")
	if err != nil || id != "r-box:claudecode" || base != "claudecode" {
		t.Fatalf("remote default = %q %q %v", id, base, err)
	}
	if id, _, err := srv.createPlatform(t.Context(), &createPathHost{}, "", ""); err != nil || id != "opencode" {
		t.Fatalf("local default = %q %v", id, err)
	}
}
