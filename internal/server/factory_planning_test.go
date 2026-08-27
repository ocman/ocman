package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestFactoryPlanningLauncherUsesLocalHostAndAppliesBoundedRules(t *testing.T) {
	var ensured string
	var created platforms.CreateSessionRequest
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
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	srv.hostRouter = hostsvc.NewRouter(&promptEnsureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir}, nil
	}})

	got, err := (factoryPlanningLauncher{server: srv}).LaunchPlanningSession(context.Background(), factory.PlanningSessionRequest{Repository: "/repo", Title: "Plan: Ship"})
	if err != nil {
		t.Fatal(err)
	}
	if got != (factory.PlanningSession{Platform: "local-agent", ID: "session-1"}) || ensured != "/repo" {
		t.Fatalf("session = %#v, ensured = %q", got, ensured)
	}
	if created != (platforms.CreateSessionRequest{Directory: "/repo", Title: "Plan: Ship", Port: "5599"}) {
		t.Fatalf("create = %#v", created)
	}
	wantRules := []platforms.PermissionRule{
		{Permission: "read", Pattern: "*", Action: "allow"},
		{Permission: "glob", Pattern: "*", Action: "allow"},
		{Permission: "grep", Pattern: "*", Action: "allow"},
		{Permission: "list", Pattern: "*", Action: "allow"},
		{Permission: "external_directory", Pattern: "*", Action: "deny"},
		{Permission: "bash", Pattern: "*", Action: "deny"},
		{Permission: "edit", Pattern: "*", Action: "deny"},
		{Permission: "task", Pattern: "*", Action: "deny"},
		{Permission: "webfetch", Pattern: "*", Action: "deny"},
	}
	if rules.SessionID != "session-1" || !reflect.DeepEqual(rules.Rules, wantRules) {
		t.Fatalf("permission rules = %#v", rules)
	}
}
