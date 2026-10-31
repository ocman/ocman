package pluginsmod

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestToolSessionPinsPermissionsAndBindingBeforeFirstPrompt(t *testing.T) {
	created := new(platformTestSessions)
	_, lookup := localPlatforms("opencode")
	queued := false
	var m *Module
	m = NewModule(Deps{Store: openStore(t), Local: func() hostsvc.Host { return stubHost{} }, Sessions: created, Platform: lookup,
		CreatePlatform: func(context.Context, hostsvc.Host, string, string) (string, string, error) {
			return "opencode", "opencode", nil
		},
		Enqueue: func(_ context.Context, _ string, _ bool, req platforms.SendMessageRequest) error {
			queued = true
			if len(created.rules) != 1 || created.rules[0][0].Action != "deny" {
				t.Fatal("prompt queued before deny-all permissions")
			}
			bindings := 0
			m.sessionTools.Range(func(_, v any) bool {
				b := v.(*sessionToolBinding)
				bindings++
				if b.session != req.SessionID || b.plugin != "documents" || !strings.Contains(req.Message, b.token) {
					t.Fatal("binding not pinned before enqueue")
				}
				return true
			})
			if bindings != 1 {
				t.Fatal("no binding at first prompt")
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	t.Cleanup(func() { cancel(); m.workers.Close() })
	root := t.TempDir()
	p := plugins.SessionRunParams{WorkspaceID: "workspace-a", Prompt: "Index documents", Archived: true, Tools: &plugins.SessionTools{Context: "run-a", MaxCalls: 2, TimeoutSeconds: 30,
		Tools: []plugins.SessionTool{{Name: "read-document", Description: "Read document", InputSchema: json.RawMessage(`{"type":"object"}`)}}}}
	if _, err := m.startSessionRun(t.Context(), "documents", plugins.Config{Project: root}, p, root, "run-1"); err != nil || !queued {
		t.Fatalf("creation %v queued=%v", err, queued)
	}
}
