package app

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/pluginsmod"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

func TestPreServingReloadAppliesSavedOfflineProofWithoutConsumingQueueOrPluginLinks(t *testing.T) {
	core := statetest.Open(t, server.ModuleSchemas()...)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	const scope = "r-offline:claudecode"
	plugins := pluginsmod.NewStore(core)
	if err := plugins.LinkPluginSession(t.Context(), scope, "legacy-native", "plugin"); err != nil {
		t.Fatal(err)
	}
	if err := core.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "held", Platform: scope, SessionID: "legacy-native", Text: "do not replay"}); err != nil {
		t.Fatal(err)
	}
	if err := prepareCanonicalSessionCore(t.Context(), core, history, platforms.NewRegistry(), nil, "hub"); err != nil {
		t.Fatal(err)
	}
	refs, err := core.SessionReferences(t.Context(), scope)
	if err != nil || len(refs) != 1 || !identity.IsPendingReference(refs[0]) {
		t.Fatalf("offline state was not quarantined: %+v %v", refs, err)
	}
	if plugin, ok, err := plugins.GetPluginSessionPlugin(t.Context(), scope, refs[0]); err != nil || !ok || plugin != "plugin" {
		t.Fatalf("offline plugin link lost: %q %v %v", plugin, ok, err)
	}
	id := "ocm_" + uuid.NewString()
	// Seed the immutable proof persisted by the authenticated reconnect worker.
	// Its owner is offline again throughout the reload below.
	if _, err := core.SQL().Exec(`UPDATE platform_session_reference SET canonical_id=? WHERE scope=? AND ref_id=?`, id, scope, refs[0]); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		registry := platforms.NewRegistry()
		if err := prepareCanonicalSessionCore(t.Context(), core, history, registry, nil, "hub"); err != nil {
			t.Fatal(err)
		}
		if !registry.CanonicalSessionIDs() || len(registry.Platforms()) != 0 {
			t.Fatal("offline proof invented an execution adapter")
		}
		queue, err := core.ListQueuedMessages(t.Context(), scope, id)
		if err != nil || len(queue) != 1 || queue[0].ID != "held" || queue[0].Text != "do not replay" {
			t.Fatalf("reload consumed held work: %+v %v", queue, err)
		}
		if plugin, ok, err := plugins.GetPluginSessionPlugin(t.Context(), scope, id); err != nil || !ok || plugin != "plugin" {
			t.Fatalf("reload lost plugin correlation: %q %v %v", plugin, ok, err)
		}
	}
}
