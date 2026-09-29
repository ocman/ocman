package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryCheckHostFake struct {
	hostsvc.Host
	got []string
}

func (h *factoryCheckHostFake) RunFactoryChecks(_ context.Context, repo, branch, target string, commands []string) (bool, string, error) {
	h.got = append([]string{repo, branch, target}, commands...)
	return true, "ok", nil
}

func TestFactoryVerificationProfileIsReadOnly(t *testing.T) {
	action := func(rules []platforms.PermissionRule, permission string) string {
		for _, rule := range rules {
			if rule.Permission == permission {
				return rule.Action
			}
		}
		return ""
	}
	if got := action(factoryImplementationRules(false), "edit"); got != "allow" {
		t.Fatalf("implementer edit = %q", got)
	}
	if got := action(factoryImplementationRules(true), "edit"); got != "deny" {
		t.Fatalf("validator edit = %q", got)
	}
	if got := action(factoryImplementationRules(true), "bash"); got != "allow" {
		t.Fatalf("validator must still run checks, bash = %q", got)
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

func TestFactoryVerificationLauncherSeams(t *testing.T) {
	var sent platforms.SendMessageRequest
	now := time.Now()
	platform := &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s", TimeUpdated: now.Add(-time.Hour).UnixMilli()}, {ID: "child", ParentID: "s", TimeUpdated: now.UnixMilli()}, {ID: "unrelated", TimeUpdated: now.Add(time.Hour).UnixMilli()}}}
	platform.sendMessageFn = func(request platforms.SendMessageRequest) error { sent = request; return nil }
	platform.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return []platforms.LivePrompt{{"id": "p"}}, nil }
	platform.sessionModelsFn = func() *platforms.SessionModelsResponse {
		return &platforms.SessionModelsResponse{Models: []platforms.SessionModel{{Provider: "p", Model: "fable-1", IsAvailable: true}}}
	}
	registry := platforms.NewRegistry()
	registry.Register(platform)
	srv := New(nil, nil, "", registry, nil)
	host := &factoryCheckHostFake{}
	srv.hostRouter = hostsvc.NewRouter(host)
	launcher := factoryImplementationLauncher{server: srv}
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

	passed, report, err := launcher.RunVerificationChecks(t.Context(), "/repo", "factory/e1", "main", []string{"make test"})
	if err != nil || !passed || report != "ok" || strings.Join(host.got, " ") != "/repo factory/e1 main make test" {
		t.Fatalf("checks = %v %q %v via %v", passed, report, err, host.got)
	}
	srv.hostRouter = hostsvc.NewRouter(&factoryImplementationHost{})
	if _, _, err := launcher.RunVerificationChecks(t.Context(), "/repo", "b", "main", nil); err == nil {
		t.Fatal("host without checks support was accepted")
	}

	if err := launcher.NotifyImplementationSession(t.Context(), session, "results"); err != nil || sent.SessionID != "s" || sent.Message != "results" {
		t.Fatalf("notify = %+v, %v", sent, err)
	}

	last, waiting, err := launcher.ImplementationActivity(t.Context(), session)
	if err != nil || !waiting || last.UnixMilli() != now.UnixMilli() {
		t.Fatalf("activity = %v %v %v", last, waiting, err)
	}
	platform.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return nil, nil }
	if _, waiting, err := launcher.ImplementationActivity(t.Context(), session); err != nil || waiting {
		t.Fatalf("no prompts = %v, %v", waiting, err)
	}
	platform.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return nil, errors.New("down") }
	if _, _, err := launcher.ImplementationActivity(t.Context(), session); err == nil {
		t.Fatal("permission lookup failure was ignored")
	}
	if _, _, err := launcher.ImplementationActivity(t.Context(), factory.PlanningSession{Platform: "opencode", ID: "gone"}); err == nil {
		t.Fatal("missing session was accepted")
	}
	platform.sessionsErr = errors.New("db down")
	if _, _, err := launcher.ImplementationActivity(t.Context(), session); err == nil {
		t.Fatal("session listing error was ignored")
	}
	if _, _, err := launcher.ImplementationActivity(t.Context(), factory.PlanningSession{Platform: "missing"}); err == nil {
		t.Fatal("missing platform was accepted")
	}
}

type promptingPlatform struct {
	*fakePlatform
	questions, refreshed []platforms.LivePrompt
}

func (p *promptingPlatform) ListQuestions(context.Context, string) ([]platforms.LivePrompt, error) {
	return p.questions, nil
}

func (p *promptingPlatform) RefreshPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return p.refreshed, nil
}

func TestFactoryImplementationActivityCountsQuestionsAndRefreshedPermissions(t *testing.T) {
	platform := &promptingPlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s"}}}}
	platform.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) {
		return []platforms.LivePrompt{{"id": "stale-cache"}}, nil
	}
	registry := platforms.NewRegistry()
	registry.Register(platform)
	launcher := factoryImplementationLauncher{server: New(nil, nil, "", registry, nil)}
	session := factory.PlanningSession{Platform: "opencode", ID: "s"}
	if _, waiting, err := launcher.ImplementationActivity(t.Context(), session); err != nil || waiting {
		t.Fatalf("refreshed permissions must replace the cache: waiting=%v, %v", waiting, err)
	}
	platform.questions = []platforms.LivePrompt{{"id": "q"}}
	if _, waiting, err := launcher.ImplementationActivity(t.Context(), session); err != nil || !waiting {
		t.Fatalf("pending question = %v, %v", waiting, err)
	}
	platform.questions, platform.refreshed = nil, []platforms.LivePrompt{{"id": "p"}}
	if _, waiting, err := launcher.ImplementationActivity(t.Context(), session); err != nil || !waiting {
		t.Fatalf("refreshed permission = %v, %v", waiting, err)
	}
}
