package owned

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type historyPlatform struct {
	*nativePlatform
	entries           []db.HistoryEntry
	snapshot          *db.HistorySnapshot
	changes           []platforms.HistoryChange
	sequence          map[string]int64
	requestedID       string
	requestedMessages []string
}

func (p *historyPlatform) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	return p.entries, nil
}
func (p *historyPlatform) HistorySnapshot(_ context.Context, id string) (*db.HistorySnapshot, error) {
	p.requestedID = id
	return p.snapshot, nil
}
func (p *historyPlatform) HistorySequences(context.Context) (map[string]int64, error) {
	return p.sequence, nil
}
func (p *historyPlatform) HistoryMessages(ctx context.Context, id string, messages []string) (*db.HistorySnapshot, error) {
	p.requestedMessages = messages
	return p.HistorySnapshot(ctx, id)
}
func (p *historyPlatform) WatchHistory(ctx context.Context, connected func(), change func(platforms.HistoryChange)) error {
	connected()
	for _, event := range p.changes {
		change(event)
	}
	return ctx.Err()
}

func historyFixture(t *testing.T) (*Adapter, *historyPlatform) {
	t.Helper()
	a, p, _ := fixture(t)
	h := &historyPlatform{nativePlatform: p, entries: []db.HistoryEntry{{ID: "hidden-child", Version: "version"}}, snapshot: &db.HistorySnapshot{
		Session:        db.Session{ID: "hidden-child", ParentID: "missing-parent", Title: "retained helper", Directory: "/repo"},
		Messages:       []db.Message{{ID: "native-message", SessionID: "hidden-child", Data: json.RawMessage(`{"role":"assistant"}`)}},
		Parts:          []db.Part{{ID: "native-part", MessageID: "native-message", SessionID: "hidden-child", Data: json.RawMessage(`{"type":"tool","input":{"sessionID":"opaque"}}`)}},
		NativeMessages: map[string]json.RawMessage{"native-message": json.RawMessage(`{"session_id":"opaque"}`)}, Subagent: true,
	}, sequence: map[string]int64{"hidden-child": 7}}
	a.native = h
	return a, h
}

func TestHistoryBoundaryIncludesHiddenHelpersAndPreservesNativeContent(t *testing.T) {
	a, p := historyFixture(t)
	entries, err := a.HistoryInventory(t.Context())
	if err != nil || len(entries) != 1 || !sessioncatalog.IsID(entries[0].ID) || entries[0].Version != "version" {
		t.Fatalf("inventory: %+v %v", entries, err)
	}
	id := entries[0].ID
	snapshot, err := a.HistorySnapshot(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if p.requestedID != "hidden-child" || snapshot.Session.ID != id || !sessioncatalog.IsID(snapshot.Session.ParentID) || snapshot.Messages[0].SessionID != id || snapshot.Parts[0].SessionID != id || !snapshot.Subagent {
		t.Fatalf("snapshot identity conversion: %+v", snapshot)
	}
	if snapshot.Messages[0].ID != "native-message" || snapshot.Parts[0].ID != "native-part" || snapshot.Parts[0].MessageID != "native-message" || string(snapshot.Parts[0].Data) != string(p.snapshot.Parts[0].Data) || string(snapshot.NativeMessages["native-message"]) != string(p.snapshot.NativeMessages["native-message"]) || p.snapshot.Session.ID != "hidden-child" || p.snapshot.Messages[0].SessionID != "hidden-child" {
		t.Fatal("history conversion mutated native content")
	}
	if _, err := a.HistorySnapshot(t.Context(), "hidden-child"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("native read bypassed boundary: %v", err)
	}
}

func TestHistoryFeedKeepsSessionKeysCanonicalAndMessageIDsNative(t *testing.T) {
	a, p := historyFixture(t)
	sequences, err := a.HistorySequences(t.Context())
	if err != nil || len(sequences) != 1 {
		t.Fatalf("sequences: %+v %v", sequences, err)
	}
	var id string
	for key, seq := range sequences {
		id = key
		if !sessioncatalog.IsID(key) || seq != 7 {
			t.Fatal(sequences)
		}
	}
	snapshot, err := a.HistoryMessages(t.Context(), id, []string{"native-message"})
	if err != nil || snapshot.Session.ID != id || p.requestedID != "hidden-child" || len(p.requestedMessages) != 1 || p.requestedMessages[0] != "native-message" {
		t.Fatalf("delta read: %+v %v", snapshot, err)
	}
	p.changes = []platforms.HistoryChange{{SessionID: "hidden-child", Seq: 8, MessageIDs: []string{"native-message"}}, {SessionID: "deleted-native", Deleted: true}}
	connected := false
	var changes []platforms.HistoryChange
	err = a.WatchHistory(t.Context(), func() { connected = true }, func(event platforms.HistoryChange) { changes = append(changes, event) })
	if err != nil || !connected || len(changes) != 2 || changes[0].SessionID != id || changes[0].MessageIDs[0] != "native-message" || !sessioncatalog.IsID(changes[1].SessionID) || !changes[1].Deleted {
		t.Fatalf("feed: %+v %v", changes, err)
	}
}

func TestHistoryInventoryFailsClosedForIncompleteOrChangedSource(t *testing.T) {
	a, p := historyFixture(t)
	p.entries = append(p.entries, p.entries[0])
	if entries, err := a.HistoryInventory(t.Context()); err == nil || entries != nil {
		t.Fatalf("duplicate inventory published: %+v %v", entries, err)
	}
	p.entries = p.entries[:1]
	p.source = "replaced"
	if entries, err := a.HistoryInventory(t.Context()); !errors.Is(err, platforms.ErrHistoryUnavailable) || entries != nil {
		t.Fatalf("replaced source published: %+v %v", entries, err)
	}
}

func TestHistoryFeedDoesNotDropUnresolvedChanges(t *testing.T) {
	a, p := historyFixture(t)
	_, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	p.changes = []platforms.HistoryChange{{SessionID: "unknown-during-create", Seq: 1}}
	called := false
	err = a.WatchHistory(t.Context(), func() {}, func(platforms.HistoryChange) { called = true })
	if !errors.Is(err, sessioncatalog.ErrCreating) || called {
		t.Fatalf("unresolved change was acknowledged: %v %v", called, err)
	}
}
