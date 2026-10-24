package pluginsmod

import (
	"context"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

type modelTestHost struct{ hostsvc.Host }

func (modelTestHost) EnsureRuntime(context.Context, string, string) (*hostsvc.EnsureRuntimeResult, error) {
	return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:1234"}, nil
}

type modelTestSessions struct {
	Sessions
	rules     []platforms.PermissionRule
	directory string
	calls     int
}

func (s *modelTestSessions) CreateConfigured(_ context.Context, _ string, request platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	s.rules, s.directory, s.calls = rules, request.Directory, s.calls+1
	return &platforms.CreateSessionResponse{ID: "model-session"}, nil
}

func TestNoToolsSessionPinsDenyRulesBeforeQueueing(t *testing.T) {
	d := openPluginStore(t)
	creator := new(modelTestSessions)
	queued := false
	m := &Module{store: new(state.DB), plugins: d, deps: Deps{
		Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator,
		Enqueue: func(_ context.Context, _ string, _ bool, request platforms.SendMessageRequest) error {
			if creator.calls != 1 || !reflect.DeepEqual(creator.rules, []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "deny"}}) {
				t.Error("prompt admitted before no-tools rules")
			}
			if request.Model != "" || request.Agent != "" || request.Message != "analyze only" {
				t.Error("host defaults not preserved")
			}
			queued = true
			return nil
		},
	}}
	root := t.TempDir()
	params := plugins.SessionRunParams{ProjectID: "project-a", NoTools: true, Prompt: "analyze only"}
	id, err := m.startSessionRun(t.Context(), "plugin", plugins.Config{Project: root}, params, root, "request-1")
	if err != nil || id != "model-session" || !queued {
		t.Fatalf("session: %s %v", id, err)
	}
	if _, err := m.startSessionRun(t.Context(), "plugin", plugins.Config{Project: root}, params, root, "request-1"); err != nil || creator.calls != 1 {
		t.Fatal("duplicate created another paid session")
	}
}
