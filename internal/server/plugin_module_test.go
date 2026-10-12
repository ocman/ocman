package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/pluginsmod"
	"github.com/NoUseFreak/ocman/internal/state"
)

// bindPlugins gives s's plugin module the lifetime context Start would.
func bindPlugins(s *Server, ctx context.Context) *Server {
	s.pluginsModule().Bind(ctx)
	return s
}

func TestConversationReconciliationRecoversMissedIdle(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	runPluginsModule(t, f.s)
	deadline := time.Now().Add(10 * time.Second)
	for f.replies() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.replies() == "" {
		t.Fatal("settled conversation was not recovered without an idle edge")
	}
}

// runPluginsModule runs s's plugin workers (outbox pump, reply
// reconciliation) until the test ends, joining them before cleanup closes
// the databases.
func runPluginsModule(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.pluginsModule().Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// ownedPlugin is a catalog row as the hub qualifies it.
type ownedPlugin struct {
	state.PluginRegistration
	OwnerID string `json:"ownerId"`
}

func encodeJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// listedPluginAction is an action row as the hub qualifies it.
type listedPluginAction struct {
	OwnerID string                   `json:"ownerId"`
	Action  plugins.ActionDescriptor `json:"action"`
}

// Plugin discovery ran before every other startup step, so the plugins
// module starts first; it exists without a state DB so its routes answer 503.
func TestPluginsModuleIsRegisteredFirst(t *testing.T) {
	mods := (&Server{}).modules()
	if _, ok := mods[0].(*pluginsmod.Module); !ok {
		t.Fatalf("first module is %s", mods[0].Name())
	}
}
