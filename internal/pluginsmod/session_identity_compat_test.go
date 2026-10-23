package pluginsmod

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/google/uuid"
)

func TestMigratedSessionRunReplaysOnlyItsLegacyWireResult(t *testing.T) {
	core, err := state.Open(statetest.Path(t, Schema), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	project := t.TempDir()
	params := plugins.SessionRunParams{Prompt: "once"}
	p := &legacyPluginPlatform{conversationLifecyclePlatform: &conversationLifecyclePlatform{}}
	m := NewModule(Deps{Store: core, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }})
	canonical := "ocm_" + uuid.NewString()
	if _, claimed, err := m.plugins.reserveSessionRun(t.Context(), "plugin", "operation", project, params); err != nil || !claimed {
		t.Fatalf("reserve: %v %v", claimed, err)
	}
	if err := m.plugins.finishSessionRun(t.Context(), "plugin", "operation", canonical, nil); err != nil {
		t.Fatal(err)
	}
	id, err := m.startSessionRun(t.Context(), "plugin", plugins.SessionConfig{Project: project}, params, filepath.Clean(project), "operation")
	if err != nil || id != "native" {
		t.Fatalf("replay changed the plugin's wire handle: %q %v", id, err)
	}
	stored, claimed, err := m.plugins.reserveSessionRun(t.Context(), "plugin", "operation", project, params)
	if err != nil || claimed || stored != canonical {
		t.Fatalf("wire projection rewrote the core receipt: %q %v %v", stored, claimed, err)
	}
	if _, err := m.pluginWireSession(t.Context(), "test", identity.PendingReference()); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("pending reference serialized to plugin: %v", err)
	}
}

type canonicalWithoutPluginCompatibility struct{ platforms.Platform }

func (*canonicalWithoutPluginCompatibility) CanonicalSessionIDs() bool { return true }

func TestCanonicalPluginWireProjectionFailsClosedWithoutCompatibility(t *testing.T) {
	id := "ocm_" + uuid.NewString()
	if _, err := pluginSessionReference(t.Context(), &canonicalWithoutPluginCompatibility{}, id); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("missing compatibility silently changed receipt namespace: %v", err)
	}
	m := NewModule(Deps{})
	if _, err := m.pluginWireSession(t.Context(), "test", id); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("missing adapter serialized canonical wire ID: %v", err)
	}
}

func TestQuarantinedSessionCompletionDoesNotSpendRetryBudget(t *testing.T) {
	core, err := state.Open(statetest.Path(t, Schema), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	m := NewModule(Deps{Store: core})
	pending := identity.PendingReference()
	id, err := m.plugins.AppendPluginSessionCompletion(t.Context(), "plugin", "r-offline:test", pending, "opaque-operation", "owed answer")
	if err != nil {
		t.Fatal(err)
	}
	m.deliverSessionCompletion(t.Context(), PluginSessionCompletion{ID: id, PluginID: "plugin", PlatformID: "r-offline:test", SessionID: pending, Text: "owed answer"})
	rows, err := m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
	if err != nil || len(rows) != 1 || rows[0].Attempts != 0 || rows[0].ID != id {
		t.Fatalf("quarantine consumed retry budget: %+v %v", rows, err)
	}
}

func TestDisabledCanonicalAdapterPausesCompletionUntilRestored(t *testing.T) {
	core, err := state.Open(statetest.Path(t, Schema), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	available := false
	p := &legacyPluginPlatform{conversationLifecyclePlatform: &conversationLifecyclePlatform{}}
	m := NewModule(Deps{Store: core, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, available }})
	canonical := "ocm_" + uuid.NewString()
	id, err := m.plugins.AppendPluginSessionCompletion(t.Context(), "plugin", "test", canonical, "operation", "owed answer")
	if err != nil {
		t.Fatal(err)
	}
	completion := PluginSessionCompletion{ID: id, PluginID: "plugin", PlatformID: "test", SessionID: canonical, Text: "owed answer"}
	for range outboxMaxAttempts + 1 {
		m.deliverSessionCompletion(t.Context(), completion)
	}
	rows, err := m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
	if err != nil || len(rows) != 1 || rows[0].Attempts != 0 {
		t.Fatalf("disabled identity spent retry budget: %+v %v", rows, err)
	}
	available = true
	if wire, err := m.pluginWireSession(t.Context(), "test", canonical); err != nil || wire != "native" {
		t.Fatalf("restoration changed wire identity: %q %v", wire, err)
	}
	// A real broker failure after projection is still a delivery attempt.
	m.deliverSessionCompletion(t.Context(), completion)
	var attempts int
	if err := core.SQL().QueryRow(`SELECT attempts FROM plugin_session_outbox WHERE id=?`, id).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("restored delivery was not attempted once: %d %v", attempts, err)
	}
}
