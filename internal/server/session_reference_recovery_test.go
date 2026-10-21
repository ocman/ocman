package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestReferenceRecoveryModuleRequiresCanonicalRegistry(t *testing.T) {
	s, _ := canonicalHTTPFixture(t)
	if s.sessionRecoveryModule() != nil {
		t.Fatal("recovery activated before canonical cutover")
	}
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	recovery := s.sessionRecoveryModule()
	if recovery == nil || recovery != s.sessionRecoveryModule() {
		t.Fatal("canonical recovery module missing or rebuilt")
	}
	found := false
	for _, feature := range s.modules() {
		found = found || feature == recovery
	}
	if !found {
		t.Fatal("recovery worker omitted from joined module lifetime")
	}
}

func TestQuarantinedLocalQueueDoesNotPreventRecoveryReload(t *testing.T) {
	core, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	pending := identity.PendingReference()
	if err := core.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "held", Platform: "opencode", SessionID: pending, Text: "retained"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{stateDB: core, registry: platforms.NewRegistry()}
	if idle, err := s.configIdle(t.Context()); err != nil || !idle {
		t.Fatalf("non-executable held queue blocked recovery: %v %v", idle, err)
	}
	queue, err := core.ListQueuedMessages(t.Context(), "opencode", pending)
	if err != nil || len(queue) != 1 || queue[0].ID != "held" {
		t.Fatalf("reload admission discarded held work: %+v %v", queue, err)
	}
}
