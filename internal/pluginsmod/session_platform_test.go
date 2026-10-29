package pluginsmod

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type platformTestSessions struct {
	Sessions
	platforms []string
	rules     [][]platforms.PermissionRule
}

func (s *platformTestSessions) CreateConfigured(_ context.Context, platformID string, _ platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	s.platforms, s.rules = append(s.platforms, platformID), append(s.rules, rules)
	return &platforms.CreateSessionResponse{ID: "ses-" + platformID}, nil
}

type platformRun struct {
	ensured, created, queued, replayed string
	request                            platforms.SendMessageRequest
	sessions                           *platformTestSessions
}

// runOnPlatforms starts one run and replays its receipt on a host whose
// default platform is fallback and whose registry holds only registered.
func runOnPlatforms(t *testing.T, fallback string, registered []string, params plugins.SessionRunParams) (platformRun, error) {
	t.Helper()
	var run platformRun
	run.sessions = new(platformTestSessions)
	_, lookup := localPlatforms(registered...)
	m := NewModule(Deps{
		Store: openStore(t), Local: func() hostsvc.Host { return stubHost{ensured: &run.ensured} }, Sessions: run.sessions,
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, requested, model string) (string, string, error) {
			requested, err := platforms.PlatformForModel(requested, model)
			if requested == "" {
				requested = fallback
			}
			return requested, requested, err
		},
		Platform: lookup,
		Enqueue: func(_ context.Context, platform string, _ bool, req platforms.SendMessageRequest) error {
			run.queued, run.request = platform, req
			return nil
		},
		Adapter: func(_ context.Context, platform, _ string) (platforms.Platform, bool) {
			run.replayed = platform
			return &conversationLifecyclePlatform{}, true
		},
	})
	project := t.TempDir()
	config := plugins.Config{Project: project}
	id, err := m.startSessionRun(t.Context(), "org.example.run", config, params, project, "op-1")
	if err != nil {
		return run, err
	}
	run.created = run.sessions.platforms[0]
	linked, ok, err := m.plugins.GetPluginSessionPlugin(t.Context(), run.created, id)
	if err != nil || !ok || linked != "org.example.run" {
		t.Fatalf("session %s not linked on %s: %v %v", id, run.created, ok, err)
	}
	replayed, err := m.startSessionRun(t.Context(), "org.example.run", config, params, project, "op-1")
	if err != nil || replayed != id || len(run.sessions.platforms) != 1 {
		t.Fatalf("replay: %q %v after %d creations", replayed, err, len(run.sessions.platforms))
	}
	return run, nil
}

func TestSessionRunUsesTheModelsPlatform(t *testing.T) {
	registered := []string{"opencode", "codex", "claudecode"}
	for _, tc := range []struct {
		name, fallback, model, want string
	}{
		{"qualified model", "opencode", "codex:openai/gpt-x", "codex"},
		{"unprefixed model is OpenCode's", "claudecode", "openai/gpt-x", "opencode"},
		{"no model takes the owner default", "claudecode", "", "claudecode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := plugins.SessionRunParams{Prompt: "review", Model: tc.model, NoTools: true}
			run, err := runOnPlatforms(t, tc.fallback, registered, params)
			if err != nil {
				t.Fatal(err)
			}
			if run.ensured != tc.want || run.created != tc.want || run.queued != tc.want || run.replayed != tc.want {
				t.Fatalf("runtime %q, created %q, queued %q, replayed %q; want %q", run.ensured, run.created, run.queued, run.replayed, tc.want)
			}
			if run.request.Model != tc.model {
				t.Fatalf("model rewritten to %q", run.request.Model)
			}
			deny := []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "deny"}}
			if !reflect.DeepEqual(run.sessions.rules[0], deny) {
				t.Fatalf("no-tools rules lost: %+v", run.sessions.rules[0])
			}
		})
	}
}

func TestSessionRunRefusesAnUnregisteredPlatform(t *testing.T) {
	params := plugins.SessionRunParams{Prompt: "review", Model: "codex:openai/gpt-x"}
	run, err := runOnPlatforms(t, "opencode", []string{"opencode"}, params)
	if !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if run.ensured != "" || len(run.sessions.platforms) != 0 || run.queued != "" {
		t.Fatalf("side effects before refusal: runtime %q, created %v, queued %q", run.ensured, run.sessions.platforms, run.queued)
	}
}

func TestSessionRunRefusesAModelItsPlatformCannotRun(t *testing.T) {
	sessions := new(platformTestSessions)
	_, lookup := localPlatforms("opencode")
	m := NewModule(Deps{
		Store: openStore(t), Local: func() hostsvc.Host { return stubHost{} }, Sessions: sessions, Platform: lookup,
		CreatePlatform: func(context.Context, hostsvc.Host, string, string) (string, string, error) {
			return "opencode", "opencode", nil
		},
	})
	project := t.TempDir()
	params := plugins.SessionRunParams{Prompt: "review", Model: "codex:openai/gpt-x"}
	_, err := m.startSessionRun(t.Context(), "p", plugins.Config{Project: project}, params, project, "op")
	if plugins.RequestError(err).Category != plugins.ErrorInvalidArgument || len(sessions.platforms) != 0 {
		t.Fatalf("err = %v, created %v", err, sessions.platforms)
	}
}

func TestLegacyRunReceiptReplaysOnOpenCode(t *testing.T) {
	d := openPluginStore(t)
	got, err := d.pluginSessionPlatform(t.Context(), "p", "unlinked")
	if err != nil || got != "opencode" {
		t.Fatalf("unlinked receipt: %q %v", got, err)
	}
	for _, platform := range []string{"codex", "claudecode"} {
		if err := d.LinkPluginSession(t.Context(), platform, "shared", "p"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.pluginSessionPlatform(t.Context(), "p", "shared"); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("ambiguous receipt: %v", err)
	}
}
