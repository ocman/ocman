package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryImplementationHost struct {
	hostsvc.Host
	request       hostsvc.WorktreeSessionRequest
	result        *hostsvc.WorktreeSessionResult
	err           error
	endpoint      string
	remoteID      string
	handoffErr    error
	handoffRepo   string
	handoffBranch string
	handoffHead   string
	upstreams     hostsvc.ProjectUpstreams
}

func (h *factoryImplementationHost) ValidateFactoryHandoff(_ context.Context, repo, branch string) (string, error) {
	h.handoffRepo, h.handoffBranch = repo, branch
	return h.handoffHead, h.handoffErr
}

func (h *factoryImplementationHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	return &h.upstreams, nil
}

func (h *factoryImplementationHost) RemoteID() string {
	if h.remoteID != "" {
		return h.remoteID
	}
	return "local"
}

func (h *factoryImplementationHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: h.endpoint}, nil
}

func (h *factoryImplementationHost) CreateWorktreeSession(_ context.Context, request hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.request = request
	return h.result, h.err
}

func TestFactoryImplementationLauncher(t *testing.T) {
	skillRules := factorySkillRulesFixture(t)
	ctx := context.Background()
	endpoint := connectedPlanningMCP(t)
	rules := []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}, {Permission: "glob", Pattern: "*", Action: "allow"}, {Permission: "grep", Pattern: "*", Action: "allow"}, {Permission: "list", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "allow"}, {Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "task", Pattern: "*", Action: "allow"}, {Permission: "external_directory", Pattern: "*", Action: "ask"}}
	rules = append(rules, skillRules...)
	rules = append(rules, platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"})

	t.Run("creates an owned bounded worktree session", func(t *testing.T) {
		host := &factoryImplementationHost{result: &hostsvc.WorktreeSessionResult{SessionID: "worktree-session"}, endpoint: endpoint}
		platform := &fakePlatform{id: "opencode", caps: platforms.Capabilities{PermissionRules: true}}
		var sent platforms.SendMessageRequest
		platform.sendMessageFn = func(req platforms.SendMessageRequest) error { sent = req; return nil }
		registry := platforms.NewRegistry()
		registry.Register(platform)
		srv := New(nil, nil, "", registry, nil)
		srv.hostRouter = hostsvc.NewRouter(host)

		request := factory.ImplementationSessionRequest{EpicID: "epic-1", WorkID: "work-1", AttemptID: "attempt-1", AgentToken: "token", Profile: "factory-implement/v1", Repository: "/repo", Branch: "factory/work", Title: "Remove dead code", Description: "Delete the obsolete helper and run its package tests."}
		got, err := (factoryImplementationLauncher{server: srv}).LaunchImplementationSession(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if got != (factory.PlanningSession{Platform: "opencode", ID: "worktree-session"}) {
			t.Fatalf("session = %#v", got)
		}
		if err := (factoryImplementationLauncher{server: srv}).PromptImplementationSession(ctx, got, request); err != nil {
			t.Fatal(err)
		}
		if host.request.ProjectDir != "/repo" || host.request.Branch != "factory/work" || host.request.Title != "implementation work-1 (@factory)" || !host.request.NewBranch || !reflect.DeepEqual(host.request.PermissionRules, rules) {
			t.Fatalf("worktree request = %#v", host.request)
		}
		if sent.SessionID != "worktree-session" || !strings.Contains(sent.Message, "Remove dead code") || !strings.Contains(sent.Message, "Delete the obsolete helper") || !strings.Contains(sent.Message, "attempt-1") || !strings.Contains(sent.Message, "token") || !strings.Contains(sent.Message, "complete_attempt") || !strings.Contains(sent.Message, "request_recovery") || !strings.Contains(sent.Message, "single pull request") || !strings.Contains(sent.Message, "clean commit") || !strings.Contains(sent.Message, "pr_url") {
			t.Fatalf("prompt = %#v", sent)
		}
		host.handoffErr = errors.New("invalid handoff")
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://example.com/pr/1", model.FactoryAttemptPolicy{}); !errors.Is(err, host.handoffErr) || host.handoffRepo != "/repo" || host.handoffBranch != "factory/epic-1" {
			t.Fatalf("handoff validation = %q, %q, %v", host.handoffRepo, host.handoffBranch, err)
		}
	})

	t.Run("validates the PR branch and pushed HEAD", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"number":1,"state":"open","html_url":"https://github.com/acme/repo/pull/1","head":{"ref":"factory/epic-1","sha":"abc123","repo":{"full_name":"acme/repo"}},"base":{"repo":{"full_name":"acme/repo"}}}`))
		}))
		defer api.Close()
		host := &factoryImplementationHost{handoffHead: "abc123", upstreams: hostsvc.ProjectUpstreams{Remotes: []forge.Remote{{Type: forge.RemoteTypeGitHub, Repo: "acme/repo"}}}}
		srv := New(nil, nil, "", platforms.NewRegistry(), nil)
		srv.hostRouter = hostsvc.NewRouter(host)
		srv.integrations.GitHub = github.NewForTest(api.URL, "token", api.Client())
		policy := model.FactoryAttemptPolicy{DeliveryRemoteType: string(forge.RemoteTypeGitHub), DeliveryRemoteRepo: "acme/repo"}
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err != nil {
			t.Fatal(err)
		}
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/other", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("accepted a PR for another branch")
		}
	})

	t.Run("does not send local skill grants to a remote host", func(t *testing.T) {
		host := &factoryImplementationHost{result: &hostsvc.WorktreeSessionResult{SessionID: "remote-session"}, endpoint: endpoint, remoteID: "remote-1"}
		registry := platforms.NewRegistry()
		srv := New(nil, nil, "", registry, nil)
		srv.hostRouter = hostsvc.NewRouter(host)

		request := factory.ImplementationSessionRequest{Profile: "factory-implement/v1", Repository: "/repo", Branch: "factory/work"}
		if _, err := (factoryImplementationLauncher{server: srv}).LaunchImplementationSession(ctx, request); err != nil {
			t.Fatal(err)
		}
		for _, rule := range host.request.PermissionRules {
			if rule.Permission == "external_directory" && rule.Pattern != "*" {
				t.Fatalf("remote request contains local grant: %#v", rule)
			}
		}
	})

	for _, tt := range []struct {
		name    string
		request factory.ImplementationSessionRequest
		hostErr error
		want    string
	}{
		{name: "rejects a wrong profile", request: factory.ImplementationSessionRequest{Profile: "wrong"}, want: "invalid Factory implementation profile"},
		{name: "wraps worktree failures", request: factory.ImplementationSessionRequest{Profile: "factory-implement/v1", Repository: "/repo"}, hostErr: errors.New("worktree failed"), want: "create Factory worktree: worktree failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host := &factoryImplementationHost{result: &hostsvc.WorktreeSessionResult{SessionID: "worktree-session"}, err: tt.hostErr, endpoint: endpoint}
			registry := platforms.NewRegistry()
			srv := New(nil, nil, "", registry, nil)
			srv.hostRouter = hostsvc.NewRouter(host)
			_, err := (factoryImplementationLauncher{server: srv}).LaunchImplementationSession(ctx, tt.request)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFactoryImplementationLauncherProbe(t *testing.T) {
	registry := platforms.NewRegistry()
	srv := New(nil, nil, "", registry, nil)
	launcher := factoryImplementationLauncher{server: srv}
	if _, err := launcher.ProbeImplementationSession(context.Background(), factory.PlanningSession{Platform: "missing", ID: "session"}); err == nil {
		t.Fatal("missing platform probe succeeded")
	}

	registry.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "session"}}})
	owned, err := launcher.ProbeImplementationSession(context.Background(), factory.PlanningSession{Platform: "opencode", ID: "session"})
	if err != nil || !owned {
		t.Fatalf("ProbeImplementationSession = %v, %v", owned, err)
	}
}

func TestFactorySessionLaunchersDelegateSessionControls(t *testing.T) {
	var disposed platforms.DisposeSessionRequest
	var replied platforms.RespondPermissionRequest
	platform := &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "session"}}}
	platform.disposeFn = func(request platforms.DisposeSessionRequest) error { disposed = request; return nil }
	platform.respondPermissionFn = func(request platforms.RespondPermissionRequest) error { replied = request; return nil }
	platform.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) {
		return []platforms.LivePrompt{{"id": "permission"}}, nil
	}
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	session := factory.PlanningSession{Platform: "opencode", ID: "session"}

	if err := (factoryImplementationLauncher{server: srv}).StopImplementationSession(context.Background(), session); err != nil || disposed.SessionID != "session" {
		t.Fatalf("StopImplementationSession = %v, %#v", err, disposed)
	}
	if err := (factoryImplementationLauncher{server: srv}).RespondImplementationPermission(context.Background(), session, "permission", "always"); err != nil || replied.SessionID != "session" || replied.PermissionID != "permission" || replied.Reply != "always" {
		t.Fatalf("RespondImplementationPermission = %v, %#v", err, replied)
	}
	if pending, err := (factoryImplementationLauncher{server: srv}).ImplementationPermissionPending(context.Background(), session, "permission"); err != nil || !pending {
		t.Fatalf("ImplementationPermissionPending = %v, %v", pending, err)
	}
	if err := (factoryPlanningLauncher{server: srv}).StopPlanningSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if owned, err := (factoryPlanningLauncher{server: srv}).ProbePlanningSession(context.Background(), session); err != nil || !owned {
		t.Fatalf("ProbePlanningSession = %v, %v", owned, err)
	}
	if _, err := (factoryPlanningLauncher{server: srv}).ProbePlanningSession(context.Background(), factory.PlanningSession{Platform: "missing"}); err == nil {
		t.Fatal("missing planning platform probe succeeded")
	}
}
