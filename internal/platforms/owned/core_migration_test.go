package owned

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestPrepareCoreMigratesLocalStateWithoutGuessingRemoteOrAmbiguousOwners(t *testing.T) {
	core := statetest.Open(t, ReferenceSchema, sessioncatalog.Schema)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	for _, source := range []statev2.Instance{
		{ID: "active", OwnerID: "owner", Platform: "test", SourceKey: "source"},
		{ID: "inactive", OwnerID: "owner", Platform: "test", SourceKey: "old-source"},
		{ID: "remote", OwnerID: "remote", Platform: "test", SourceKey: "remote-source"},
	} {
		if err := history.RegisterInstance(t.Context(), source); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"collision", source.ID + "-unique"} {
			if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: source.ID, SessionID: id}, Directory: "/repo", Title: id}, Messages: []statev2.Message{{ID: "message", Role: "assistant"}}, Parts: []statev2.Part{{ID: "part", MessageID: "message", Kind: "text", Text: "retained"}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, key := range []state.Key{
		{Platform: "test", SessionID: "native"}, {Platform: "test", SessionID: "collision"}, {Platform: "test", SessionID: "inactive-unique"},
		{Platform: "r-remote:test", SessionID: "native"}, {Platform: "", SessionID: "native"},
	} {
		if err := core.PinSession(t.Context(), key.Platform, key.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "queue", Platform: "test", SessionID: "native", Text: "preserved"}); err != nil {
		t.Fatal(err)
	}
	routine := state.Routine{ID: "routine", Name: "routine", Prompt: "retained", Directory: "/repo", RemoteID: "local", Platform: "test", SessionID: "native", SessionMode: "new", ScheduleKind: "interval", ScheduleConfigJSON: `{"minutes":15}`, CreatedAt: 1, UpdatedAt: 1}
	if err := core.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().Exec(`INSERT INTO routine_run (id,routine_id,routine_updated_at,routine_name,prompt,directory,remote_id,trigger,state,occurrence_at,created_at,platform,session_id,session_mode) VALUES ('run','routine',1,'routine','retained','/repo','local','manual','running',1,1,'test','native','new')`); err != nil {
		t.Fatal(err)
	}
	native := &nativePlatform{id: "test", source: "source", rows: []db.Session{{ID: "native", Directory: "/repo"}, {ID: "collision", Directory: "/repo"}}}
	adapters, err := PrepareCore(t.Context(), core, history, catalogue, "owner", []platforms.Integration{native})
	if err != nil || len(adapters) != 1 {
		t.Fatalf("prepare: %+v %v", adapters, err)
	}
	bound, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "native"})
	if err != nil || bound.Origin != sessioncatalog.Routine || bound.OriginID != "routine" || !bound.LegacyKeys {
		t.Fatalf("legacy provenance lost: %+v %v", bound, err)
	}
	queue, err := core.ListQueuedMessages(t.Context(), "test", bound.ID)
	if err != nil || len(queue) != 1 || queue[0].Text != "preserved" {
		t.Fatalf("queue: %+v %v", queue, err)
	}
	refs, _ := NewReferences(core)
	page, err := refs.referencePage(t.Context(), "test", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range page {
		if ref.NativeID == "collision" && ref.CanonicalID != "" {
			t.Fatal("active source stole ambiguous historical association")
		}
		if ref.NativeID == "inactive-unique" {
			if !sessioncatalog.IsID(ref.CanonicalID) {
				t.Fatal("unique inactive association was not retained")
			}
			if _, err := adapters[0].(*Adapter).nativeID(t.Context(), ref.CanonicalID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
				t.Fatalf("inactive source gained execution: %v", err)
			}
		}
	}
	for _, scope := range []string{"r-remote:test", ""} {
		pending, err := core.SessionReferences(t.Context(), scope)
		if scope == "" {
			pending, err = core.UnattributedSessionReferences(t.Context())
		}
		if err != nil || len(pending) != 1 || !identity.IsPendingReference(pending[0]) {
			t.Fatalf("unknown owner assigned a session: %q %+v %v", scope, pending, err)
		}
	}
	if _, err := history.ReadConversation(t.Context(), statev2.Key{InstanceID: "remote", SessionID: "remote-unique"}, 10, 0); err != nil {
		t.Fatalf("hub rekeyed a remote owner's history: %v", err)
	}
	before, err := catalogue.List(t.Context(), "owner", 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCore(t.Context(), core, history, catalogue, "owner", []platforms.Integration{native}); err != nil {
		t.Fatal(err)
	}
	after, err := catalogue.List(t.Context(), "owner", 500, 0)
	if err != nil || len(before) != len(after) {
		t.Fatalf("restart allocated more identities: before=%d after=%d %v", len(before), len(after), err)
	}
	if row, err := catalogue.Get(t.Context(), bound.ID); err != nil || row.Origin != sessioncatalog.Routine || row.NativeID != "native" {
		t.Fatalf("restart changed provenance: %+v %v", row, err)
	}
}
