package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestSessionCatalogueBackfillsLegacyOriginOnlyOnSelectedSource(t *testing.T) {
	durable, err := state.Open(statetest.Path(t, ModuleSchemas()...), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = durable.Close() }()
	history, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	for _, source := range []string{"source", "inactive-source"} {
		instance := statev2.Instance{ID: source, OwnerID: "owner", Platform: "test", SourceKey: source}
		if err := history.RegisterInstance(t.Context(), instance); err != nil {
			t.Fatal(err)
		}
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: source, SessionID: "session"}, Title: "retained", Directory: "/repo"}}); err != nil {
			t.Fatal(err)
		}
	}
	routine := state.Routine{ID: "routine", Platform: "test", Name: "routine", Prompt: "prompt", Directory: "/repo", RemoteID: "local", SessionMode: "new", ScheduleKind: "manual", PermissionRulesJSON: "[]", ScheduleConfigJSON: "{}"}
	if err := durable.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := durable.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: routine.ID, State: "running", Trigger: "manual", OccurrenceAt: 1, CreatedAt: 1}); err != nil || !claimed {
		t.Fatalf("routine fixture: %v %v", claimed, err)
	}
	if err := durable.LinkRoutineRun(t.Context(), "run", "test", "session", 1, false); err != nil {
		t.Fatal(err)
	}
	if err := durable.PinSession(t.Context(), "test", "session"); err != nil {
		t.Fatal(err)
	}
	if err := durable.MarkSessionSeen(t.Context(), "test", "session", 77); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(&historyTestPlatform{fakePlatform: &fakePlatform{id: "test"}})
	s := New(nil, durable, "", registry, nil).WithConversationHistory(history, "owner")
	if s.sessionCatalogue == nil {
		t.Fatal("session catalogue not installed with history")
	}
	registered := false
	for _, m := range s.modules() {
		if m == s.sessionCatalogue {
			registered = true
		}
	}
	if !registered {
		t.Fatal("catalogue missing from module lifecycle")
	}
	if err := s.sessionCatalogue.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(durable)
	if err != nil {
		t.Fatal(err)
	}
	current, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "session"})
	if err != nil || current.Origin != sessioncatalog.Routine || current.OriginID != routine.ID {
		t.Fatalf("legacy routine origin lost: %+v %v", current, err)
	}
	inactive, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "inactive-source", NativeID: "session"})
	if err != nil || inactive.ID == current.ID || inactive.Origin != sessioncatalog.External {
		t.Fatalf("native-only provenance guessed across sources: %+v %v", inactive, err)
	}
	pins, err := durable.PinnedSessions(t.Context())
	if err != nil || len(pins) != 1 || pins[state.Key{Platform: "test", SessionID: "session"}] == 0 {
		t.Fatalf("migration changed pin state: %+v %v", pins, err)
	}
	seen, err := durable.SeenSessions(t.Context())
	if err != nil || seen[state.Key{Platform: "test", SessionID: "session"}] != 77 {
		t.Fatalf("migration changed seen state: %+v %v", seen, err)
	}
}
