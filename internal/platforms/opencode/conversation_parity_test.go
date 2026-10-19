package opencode

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/conversationread"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestStoredConversationMatchesOpenCodePages(t *testing.T) {
	database := newTestDBWithSessions(t, []testSession{{id: "s", directory: "/repo", messages: []string{
		`{"role":"user","model":{"providerID":"p","modelID":"m"}}`,
		`{"role":"assistant","finish":"stop","tokens":{"input":2,"output":3},"time":{"created":1001,"completed":1002}}`,
	}}, {id: "other", directory: "/repo", messages: []string{`{"role":"assistant","agent":"build","providerID":"p","modelID":"default","finish":"stop"}`}}})
	a := New(database, nil)
	identity, err := a.HistoryIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "physical", OwnerID: "owner", Platform: identity.Platform, SourceKey: identity.SourceKey}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s", "other"} {
		snapshot, err := a.HistorySnapshot(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "physical", SessionID: id}, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
		if err := store.SetSourceState(t.Context(), "physical", "read-ready/"+id, snapshot.Version); err != nil {
			t.Fatal(err)
		}
	}
	reader := &conversationread.Service{Store: store, OwnerID: "owner"}
	for _, page := range [][2]int{{30, 0}, {1, 0}, {1, 1}, {0, 0}} {
		before, err := a.Session(t.Context(), "s", page[0], page[1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := a.Session(platforms.WithConversationReader(t.Context(), reader), "s", page[0], page[1])
		if err != nil {
			t.Fatal(err)
		}
		if after.ConversationSource != "statev2" || after.TotalMessages != before.TotalMessages || after.ContextTokenCount != before.ContextTokenCount || after.DefaultAgent != before.DefaultAgent || after.DefaultModel != before.DefaultModel {
			t.Fatalf("page %v metadata changed: %+v vs %+v", page, after, before)
		}
		if len(after.Messages) != len(before.Messages) || len(after.Parts) != len(before.Parts) {
			t.Fatal("page content counts changed")
		}
		for i := range before.Messages {
			var oldData, newData any
			_ = json.Unmarshal(before.Messages[i].Data, &oldData)
			_ = json.Unmarshal(after.Messages[i].Data, &newData)
			if before.Messages[i].ID != after.Messages[i].ID || before.Messages[i].TimeCreated != after.Messages[i].TimeCreated || !reflect.DeepEqual(oldData, newData) {
				t.Fatal("message wire metadata changed")
			}
		}
	}
}
