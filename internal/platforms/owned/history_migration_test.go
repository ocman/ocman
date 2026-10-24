package owned

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestHistoryMigrationRetainsInactiveSourcesAndReplaysWithoutNewIdentities(t *testing.T) {
	_, _, catalogue := fixture(t)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	for _, source := range []statev2.Instance{
		{ID: "active", OwnerID: "owner", Platform: "test", SourceKey: "source"},
		{ID: "inactive", OwnerID: "owner", Platform: "test", SourceKey: "old-source"},
		{ID: "foreign", OwnerID: "other-owner", Platform: "test", SourceKey: "source"},
	} {
		if err := history.RegisterInstance(t.Context(), source); err != nil {
			t.Fatal(err)
		}
		batch := statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: source.ID, SessionID: "same-native"}, ParentID: "missing-parent", Title: source.ID, Directory: "/repo", Extensions: json.RawMessage(`{"ParentID":"missing-parent"}`)}, Messages: []statev2.Message{{ID: "native-message", Role: "assistant", ConversationVisible: true}}, Parts: []statev2.Part{{ID: "native-part", MessageID: "native-message", Kind: "text", Text: "retained content"}}}
		if _, err := history.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateHistory(t.Context(), catalogue, history, "owner"); err != nil {
		t.Fatal(err)
	}
	identities := map[string]string{}
	for _, source := range []string{"source", "old-source"} {
		row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: source, NativeID: "same-native"})
		if err != nil {
			t.Fatal(err)
		}
		identities[source] = row.ID
		instance := "active"
		if source == "old-source" {
			instance = "inactive"
		}
		page, err := history.ReadConversation(t.Context(), statev2.Key{InstanceID: instance, SessionID: row.ID}, 20, 0)
		if err != nil || page.Session.ParentID != row.ParentID || page.Messages[0].ID != "native-message" || page.Parts[0].Text != "retained content" {
			t.Fatalf("retained source %s: %+v %v", source, page, err)
		}
	}
	if identities["source"] == identities["old-source"] {
		t.Fatal("inactive source stole active identity")
	}
	if _, err := history.ReadConversation(t.Context(), statev2.Key{InstanceID: "foreign", SessionID: "same-native"}, 20, 0); err != nil {
		t.Fatalf("foreign owner changed: %v", err)
	}
	before, err := catalogue.List(t.Context(), "owner", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateHistory(t.Context(), catalogue, history, "owner"); err != nil {
		t.Fatal(err)
	}
	after, err := catalogue.List(t.Context(), "owner", 100, 0)
	if err != nil || len(before) != len(after) || len(after) != 4 {
		t.Fatalf("replay created new bindings: %+v %v", after, err)
	}
}
