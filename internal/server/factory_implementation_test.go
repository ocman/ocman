package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/git"
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
	branches      []string
	worktrees     []git.Worktree
	worktreeErr   error
	target        string
	targetErr     error
}

func (h *factoryImplementationHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	return h.worktrees, h.worktreeErr
}

func (h *factoryImplementationHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) {
	return h.target, h.targetErr
}

func TestFactoryResolvesLegacyWorkspaceWithoutForge(t *testing.T) {
	for _, name := range []string{"shared", "successor", "unrelated branch", "different worktree", "missing detail", "missing session", "missing directory", "session error", "missing platform", "worktree error", "target error"} {
		t.Run(name, func(t *testing.T) {
			detail := &platforms.SessionDetail{Session: &db.Session{Directory: "/worktree"}}
			var sessionErr error
			host := &factoryImplementationHost{target: "main", worktrees: []git.Worktree{{Path: "/worktree", Branch: "factory/epic"}}}
			switch name {
			case "successor":
				host.worktrees[0].Branch = "factory/epic-2"
			case "unrelated branch":
				host.worktrees[0].Branch = "factory/other"
			case "different worktree":
				host.worktrees[0].Path = "/other"
			case "missing detail":
				detail = nil
			case "missing session":
				detail.Session = nil
			case "missing directory":
				detail.Session.Directory = ""
			case "session error":
				sessionErr = errors.New("session unavailable")
			case "worktree error":
				host.worktreeErr = errors.New("owner disconnected")
			case "target error":
				host.targetErr = errors.New("target unavailable")
			}
			registry := platforms.NewRegistry()
			if name != "missing platform" {
				registry.Register(&fakePlatform{id: "test", sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, sessionErr }})
			}
			srv := New(nil, nil, "", registry, nil)
			srv.hostRouter = hostsvc.NewRouter(host)
			branch, target, err := (factoryImplementationLauncher{server: srv}).ResolveImplementationWorkspace(t.Context(), "/repo", "factory/epic", factory.PlanningSession{Platform: "test", ID: "old"})
			if name == "shared" || name == "successor" {
				if err != nil || branch != host.worktrees[0].Branch || target != "main" {
					t.Fatalf("workspace = %q/%q, %v", branch, target, err)
				}
			} else if err == nil {
				t.Fatalf("accepted invalid workspace = %q/%q", branch, target)
			}
		})
	}
}

func TestFactoryResolvesDeletedForgejoBranch(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"number":604,"state":"closed","merged":true,"html_url":"https://forge.example/dries/ocman/pulls/604","head":{"ref":"refs/pull/604/head","label":"factory/aamruifdam-crov-2","sha":"ad222f13c6acde6a9874bf93bd2e25bd97698630","repo":{"full_name":"dries/ocman"}},"base":{"repo":{"full_name":"dries/ocman"}}}`)
	}))
	defer api.Close()
	srv := New(nil, nil, "", platforms.NewRegistry(), nil)
	srv.hostRouter = hostsvc.NewRouter(&factoryImplementationHost{})
	srv.integrations.Forgejo = forgejo.NewRegistryForTest(map[string]*forgejo.Client{
		"forge.example": forgejo.NewForTest("forge.example", api.URL, "token", api.Client()),
	})
	policy := model.FactoryAttemptPolicy{DeliveryRemoteType: "forgejo", DeliveryRemoteHost: "forge.example", DeliveryRemoteRepo: "dries/ocman"}
	branch, base, err := (factoryImplementationLauncher{server: srv}).ResolveImplementationBranch(t.Context(), "/repo", "factory/aamruifdam-crov", "https://forge.example/dries/ocman/pulls/604", policy)
	if err != nil || branch != "factory/aamruifdam-crov-3" || base != "" {
		t.Fatalf("replacement branch/base = %q/%q, %v", branch, base, err)
	}
}

func (h *factoryImplementationHost) ValidateFactoryHandoff(_ context.Context, repo, branch string) (string, error) {
	h.handoffRepo, h.handoffBranch = repo, branch
	return h.handoffHead, h.handoffErr
}

func (h *factoryImplementationHost) PrepareFactoryWorkspace(_ context.Context, _, _, _, target string) (string, string, error) {
	return target, "", h.handoffErr
}

func (h *factoryImplementationHost) ValidateFactoryCheckpoint(_ context.Context, _, _, _ string) (string, error) {
	return h.handoffHead, h.handoffErr
}

func TestFactoryDeliveryValidatesFinalPR(t *testing.T) {
	for _, provider := range []string{"forgejo", "github"} {
		t.Run(provider, func(t *testing.T) {
			branch, target, head, state, fork, draft, merged := "factory/epic", "main", "abc123", "open", false, false, false
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				headRepo := "acme/repo"
				mergedAt := "null"
				if fork {
					headRepo = "other/repo"
				}
				if merged {
					mergedAt = `"2026-09-12T00:00:00Z"`
				}
				_, _ = fmt.Fprintf(w, `{"number":1,"state":%q,"draft":%t,"merged":%t,"merged_at":%s,"html_url":"https://forge.example/acme/repo/pulls/1","head":{"ref":%q,"sha":%q,"repo":{"full_name":%q}},"base":{"ref":%q,"repo":{"full_name":"acme/repo"}}}`, state, draft, merged, mergedAt, branch, head, headRepo, target)
			}))
			defer api.Close()
			srv := New(nil, nil, "", platforms.NewRegistry(), nil)
			host := &factoryImplementationHost{handoffHead: "abc123"}
			srv.hostRouter = hostsvc.NewRouter(host)
			srv.integrations.Forgejo = forgejo.NewRegistryForTest(map[string]*forgejo.Client{"forge.example": forgejo.NewForTest("forge.example", api.URL, "token", api.Client())})
			srv.integrations.GitHub = github.NewForTest(api.URL, "token", api.Client())
			launcher := factoryImplementationLauncher{server: srv}
			policy := model.FactoryAttemptPolicy{Delivery: true, TargetBranch: "main", DeliveryRemoteType: provider, DeliveryRemoteHost: "forge.example", DeliveryRemoteRepo: "acme/repo"}
			validate := func() error {
				return launcher.ValidateImplementationHandoff(t.Context(), "/repo", "factory/epic", "", "https://forge.example/acme/repo/pulls/1", policy)
			}
			if err := validate(); err != nil {
				t.Fatal(err)
			}
			if status, observedHead, err := launcher.ObserveImplementationDelivery(t.Context(), "https://forge.example/acme/repo/pulls/1", policy); err != nil || status != "open" || observedHead != "abc123" {
				t.Fatalf("delivery observation = %q/%q, %v", status, observedHead, err)
			}
			for _, change := range []func(){func() { target = "wrong" }, func() { branch = "wrong" }, func() { head = "wrong" }, func() { state = "closed" }, func() { fork = true }, func() { draft = true }} {
				change()
				if err := validate(); err == nil {
					t.Fatal("accepted invalid delivery PR")
				}
				branch, target, head, state, fork, draft, merged = "factory/epic", "main", "abc123", "open", false, false, false
			}
			state, merged, branch, policy.ForceComplete = "closed", true, "factory/epic-old", true
			if err := validate(); err != nil {
				t.Fatalf("force-complete merged PR: %v", err)
			}
			target = "wrong"
			if err := validate(); err == nil {
				t.Fatal("force-completed a merged PR into another target")
			}
			state, merged, branch, policy.ForceComplete = "open", false, "factory/epic", false
			target = "main"
			if target, _, err := launcher.PrepareImplementationWorkspace(t.Context(), "/repo", branch, "", "main"); err != nil || target != "main" {
				t.Fatalf("workspace = %q, %v", target, err)
			}
			if head, err := launcher.ValidateImplementationCheckpoint(t.Context(), "/repo", branch, ""); err != nil || head != "abc123" {
				t.Fatalf("checkpoint = %q, %v", head, err)
			}
			srv.hostRouter = hostsvc.NewRouter(&struct{ hostsvc.Host }{})
			if _, _, err := launcher.PrepareImplementationWorkspace(t.Context(), "/repo", branch, "", "main"); err == nil {
				t.Fatal("accepted host without workspace capability")
			}
			if _, err := launcher.ValidateImplementationCheckpoint(t.Context(), "/repo", branch, ""); err == nil {
				t.Fatal("accepted host without checkpoint capability")
			}
		})
	}
	srv := New(nil, nil, "", platforms.NewRegistry(), nil)
	if _, _, err := (factoryImplementationLauncher{server: srv}).ObserveImplementationDelivery(t.Context(), "https://forge.example/acme/repo/pulls/1", model.FactoryAttemptPolicy{}); err == nil {
		t.Fatal("observed delivery without a forge target")
	}
}

func (h *factoryImplementationHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	return &h.upstreams, nil
}

func (h *factoryImplementationHost) GitBranches(context.Context, string) ([]string, error) {
	return h.branches, nil
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
	rules := []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}, {Permission: "glob", Pattern: "*", Action: "allow"}, {Permission: "grep", Pattern: "*", Action: "allow"}, {Permission: "list", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "allow"}, {Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "task", Pattern: "*", Action: "allow"}, {Permission: "external_directory", Pattern: "*", Action: "ask"}, {Permission: "external_directory", Pattern: filepath.Join("/other", "**"), Action: "allow"}, {Permission: "edit", Pattern: filepath.Join("/other", "**"), Action: "deny"}}
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

		request := factory.ImplementationSessionRequest{EpicID: "epic-1", WorkID: "work-1", AttemptID: "attempt-1", AgentToken: "token", Profile: "factory-implement/v1", Repository: "/repo", Projects: []string{"/repo", "/other"}, Branch: "factory/work", BaseRef: "factory/previous", Title: "Remove dead code", Description: "Delete the obsolete helper and run its package tests."}
		got, err := (factoryImplementationLauncher{server: srv}).LaunchImplementationSession(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if got != (factory.PlanningSession{Platform: "opencode", ID: "worktree-session"}) {
			t.Fatalf("session = %#v", got)
		}
		request.Model = "openai/gpt-5.6-sol"
		if err := (factoryImplementationLauncher{server: srv}).PromptImplementationSession(ctx, got, request); err != nil {
			t.Fatal(err)
		}
		if sent.Model != request.Model {
			t.Fatalf("model = %q", sent.Model)
		}
		if host.request.ProjectDir != "/repo" || host.request.Branch != "factory/work" || host.request.BaseRef != "factory/previous" || host.request.Title != "IMPL work-1 (@factory)" || !host.request.NewBranch || !reflect.DeepEqual(host.request.PermissionRules, rules) {
			t.Fatalf("worktree request = %#v", host.request)
		}
		if sent.SessionID != "worktree-session" || !strings.Contains(sent.Message, "Remove dead code") || !strings.Contains(sent.Message, "Delete the obsolete helper") || !strings.Contains(sent.Message, "attempt-1") || !strings.Contains(sent.Message, "token") || !strings.Contains(sent.Message, "complete_attempt") || !strings.Contains(sent.Message, "request_recovery") || !strings.Contains(sent.Message, "Do not create a pull request") || !strings.Contains(sent.Message, "Omit pr_url") || !strings.Contains(sent.Message, "clean commit") {
			t.Fatalf("prompt = %#v", sent)
		}
		request.Delivery, request.TargetBranch = true, "main"
		if err := (factoryImplementationLauncher{server: srv}).PromptImplementationSession(ctx, got, request); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"Deliver Factory Work Epic", "into main", "reuse it on retries", "pr_url", "Never merge"} {
			if !strings.Contains(sent.Message, text) {
				t.Fatalf("delivery prompt missing %q: %s", text, sent.Message)
			}
		}
	})

	t.Run("validates the PR branch and pushed HEAD", func(t *testing.T) {
		state := "open"
		draft := false
		prBranch := "factory/epic-1"
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"number":1,"state":%q,"draft":%t,"merged":%t,"html_url":"https://github.com/acme/repo/pull/1","head":{"ref":%q,"sha":"abc123","repo":{"full_name":"acme/repo"}},"base":{"repo":{"full_name":"acme/repo"}}}`, state, draft, state == "merged", prBranch)
		}))
		defer api.Close()
		host := &factoryImplementationHost{handoffHead: "abc123", upstreams: hostsvc.ProjectUpstreams{Remotes: []forge.Remote{{Type: forge.RemoteTypeGitHub, Repo: "acme/repo"}}}}
		srv := New(nil, nil, "", platforms.NewRegistry(), nil)
		srv.hostRouter = hostsvc.NewRouter(host)
		srv.integrations.GitHub = github.NewForTest(api.URL, "token", api.Client())
		policy := model.FactoryAttemptPolicy{DeliveryRemoteType: string(forge.RemoteTypeGitHub), DeliveryRemoteRepo: "acme/repo"}
		host.handoffErr = errors.New("invalid handoff")
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "", "https://github.com/acme/repo/pull/1", policy); !errors.Is(err, host.handoffErr) {
			t.Fatalf("handoff error = %v", err)
		}
		host.handoffErr = nil
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "", "https://github.com/acme/repo/pull/1", policy); err != nil {
			t.Fatal(err)
		}
		draft = true
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "", "https://github.com/acme/repo/pull/1", policy); err == nil || !strings.Contains(err.Error(), "ready for review") {
			t.Fatalf("draft PR error = %v", err)
		}
		draft = false
		prBranch = "factory/epic-1-2"
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("accepted a rotated first PR")
		}
		prBranch = "factory/epic-1"
		state = "merged"
		prBranch = "refs/pull/1/head"
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "", "https://github.com/acme/repo/pull/1", policy); err != nil {
			t.Fatalf("merged PR: %v", err)
		}
		state = "closed"
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("accepted an unmerged closed PR")
		}
		state = "open"
		prBranch = "factory/epic-1"
		if err := (factoryImplementationLauncher{server: srv}).ValidateImplementationHandoff(ctx, "/repo", "factory/other", "", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("accepted a PR for another branch")
		}
	})

	t.Run("accepts a replacement branch after the shared PR closes", func(t *testing.T) {
		previousState := "merged"
		previousBranch := "factory/epic-1"
		previousHead := "old"
		replacementBranch := "factory/epic-1-2"
		convertedToDraft := 0
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/graphql" {
				convertedToDraft++
				_, _ = w.Write([]byte(`{"data":{"convertPullRequestToDraft":{"pullRequest":{"isDraft":true}}}}`))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/1") {
				_, _ = fmt.Fprintf(w, `{"number":1,"node_id":"PR_node","state":%q,"merged":%t,"html_url":"https://github.com/acme/repo/pull/1","head":{"ref":%q,"sha":%q,"repo":{"full_name":"acme/repo"}},"base":{"repo":{"full_name":"acme/repo"}}}`, previousState, previousState == "merged", previousBranch, previousHead)
				return
			}
			_, _ = fmt.Fprintf(w, `{"number":2,"state":"open","merged":false,"html_url":"https://github.com/acme/repo/pull/2","head":{"ref":%q,"sha":"new","repo":{"full_name":"acme/repo"}},"base":{"repo":{"full_name":"acme/repo"}}}`, replacementBranch)
		}))
		defer api.Close()
		host := &factoryImplementationHost{handoffHead: "new"}
		srv := New(nil, nil, "", platforms.NewRegistry(), nil)
		srv.hostRouter = hostsvc.NewRouter(host)
		srv.integrations.GitHub = github.NewForTest(api.URL, "token", api.Client())
		policy := model.FactoryAttemptPolicy{DeliveryRemoteType: string(forge.RemoteTypeGitHub), DeliveryRemoteRepo: "acme/repo"}
		launcher := factoryImplementationLauncher{server: srv}
		if branch, baseRef, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err != nil || branch != "factory/epic-1-2" || baseRef != "" {
			t.Fatalf("resolved replacement branch/base = %q/%q, %v", branch, baseRef, err)
		}
		host.branches = []string{"factory/epic-1-2"}
		if _, _, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("existing replacement branch error = %v", err)
		}
		host.branches = nil
		previousHead = ""
		if branch, baseRef, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err != nil || branch != "factory/epic-1-2" || baseRef != "" {
			t.Fatalf("merged target base = %q/%q, %v", branch, baseRef, err)
		}
		previousHead = "old"
		if err := launcher.ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", "https://github.com/acme/repo/pull/2", policy); err != nil || host.handoffBranch != "factory/epic-1-2" {
			t.Fatalf("replacement handoff branch = %q, err = %v", host.handoffBranch, err)
		}
		previousState = "closed"
		if err := launcher.ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", "https://github.com/acme/repo/pull/2", policy); err != nil {
			t.Fatalf("replacement after closed PR: %v", err)
		}
		previousState = "open"
		previousBranch = "unrelated"
		if _, _, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("resolved an unrelated open PR")
		}
		if convertedToDraft != 0 {
			t.Fatalf("converted unrelated PR to draft %d times", convertedToDraft)
		}
		previousBranch = "factory/epic-1"
		if branch, baseRef, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err != nil || branch != "factory/epic-1" || baseRef != "" {
			t.Fatalf("resolved open PR branch/base = %q/%q, %v", branch, baseRef, err)
		}
		if convertedToDraft != 1 {
			t.Fatalf("draft conversions = %d", convertedToDraft)
		}
		if err := launcher.ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", "https://github.com/acme/repo/pull/2", policy); err == nil {
			t.Fatal("replaced an open shared PR")
		}
		previousState, previousBranch = "merged", "factory/epic-1-2"
		if branch, baseRef, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err != nil || branch != "factory/epic-1-3" || baseRef != "" {
			t.Fatalf("resolved second replacement branch/base = %q/%q, %v", branch, baseRef, err)
		}
		if err := launcher.ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("reused a merged pull request")
		}
		replacementBranch = "factory/epic-1-lookalike"
		if err := launcher.ValidateImplementationHandoff(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", "https://github.com/acme/repo/pull/2", policy); err == nil {
			t.Fatal("accepted a non-numbered replacement branch")
		}
		previousState = "unknown"
		if _, _, err := launcher.ResolveImplementationBranch(ctx, "/repo", "factory/epic-1", "https://github.com/acme/repo/pull/1", policy); err == nil {
			t.Fatal("resolved a branch from an unknown PR status")
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
	var sent platforms.SendMessageRequest
	sends := 0
	detail := &platforms.SessionDetail{}
	platform := &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "session"}}}
	platform.disposeFn = func(request platforms.DisposeSessionRequest) error { disposed = request; return nil }
	platform.respondPermissionFn = func(request platforms.RespondPermissionRequest) error { replied = request; return nil }
	platform.sendMessageFn = func(request platforms.SendMessageRequest) error { sent = request; sends++; return nil }
	platform.sessionDetailFn = func(string) (*platforms.SessionDetail, error) { return detail, nil }
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
	if err := (factoryImplementationLauncher{server: srv}).ResumeImplementationSession(context.Background(), session, "gate-1", "Use A"); err != nil || sent.SessionID != "session" || !strings.Contains(sent.Message, "gate-1") || !strings.Contains(sent.Message, "Use A") {
		t.Fatalf("ResumeImplementationSession = %v, %#v", err, sent)
	}
	detail.Parts = []db.Part{{Data: []byte(`{"type":"text","text":"Factory recovery response for gate gate-1:"}`)}}
	if err := (factoryImplementationLauncher{server: srv}).ResumeImplementationSession(context.Background(), session, "gate-1", "Use A"); err != nil || sends != 1 {
		t.Fatalf("idempotent ResumeImplementationSession = %v, sends %d", err, sends)
	}
	detail.Parts = nil
	platform.sendMessageFn = func(request platforms.SendMessageRequest) error {
		sends++
		detail.Parts = []db.Part{{Data: []byte(request.Message)}}
		return errors.New("response lost after acceptance")
	}
	if err := (factoryImplementationLauncher{server: srv}).ResumeImplementationSession(context.Background(), session, "gate-2", "Use B"); err == nil {
		t.Fatal("ambiguous delivery returned success")
	}
	if err := (factoryImplementationLauncher{server: srv}).ResumeImplementationSession(context.Background(), session, "gate-2", "Use B"); err != nil || sends != 2 {
		t.Fatalf("ambiguous delivery retry = %v, total sends %d", err, sends)
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
