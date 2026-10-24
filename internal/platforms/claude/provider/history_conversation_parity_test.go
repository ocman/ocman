package claudeprovider

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

func TestHistoryConversationMatchesNativePagedContent(t *testing.T) {
	idx := openSynced(t, fixture(t))
	reader := &HistoryReader{index: idx}
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "claude", OwnerID: "owner", Platform: "claudecode", SourceKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{mainID, childID, otherID} {
		snapshot, err := reader.Snapshot(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		key := statev2.Key{InstanceID: "claude", SessionID: id}
		batch, _, err := conversationingest.Normalize(key, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
		for _, paging := range []string{"limit=30&offset=0", "limit=1&offset=0", "limit=1&offset=1", "limit=0&offset=0"} {
			native := get[platforms.SessionDetail](t, idx, "/sessions/"+id+"?"+paging)
			limit, offset := 30, 0
			switch paging {
			case "limit=1&offset=0":
				limit = 1
			case "limit=1&offset=1":
				limit, offset = 1, 1
			case "limit=0&offset=0":
				limit = 0
			}
			page, err := store.ReadConversation(t.Context(), key, limit, offset)
			if err != nil {
				t.Fatal(err)
			}
			converted, err := conversationread.Convert(page, &platforms.ConversationHead{Session: native.Session, DefaultModel: native.DefaultModel}, "claudecode")
			if err != nil {
				t.Fatal(err)
			}
			if converted.TotalMessages != native.TotalMessages || converted.ContextTokenCount != native.ContextTokenCount || converted.DefaultModel != native.DefaultModel {
				t.Fatalf("%s %s metadata differs: %+v native %+v", id, paging, converted, native)
			}
			assertJSON := func(name string, a, b any) {
				t.Helper()
				rawA, _ := json.Marshal(a)
				rawB, _ := json.Marshal(b)
				var valueA, valueB any
				_ = json.Unmarshal(rawA, &valueA)
				_ = json.Unmarshal(rawB, &valueB)
				if !reflect.DeepEqual(valueA, valueB) {
					t.Fatalf("%s %s %s differs:\n%s\n%s", id, paging, name, rawA, rawB)
				}
			}
			// Both APIs promise empty arrays to clients, even if an internal
			// native page uses nil slices when it contains no messages/parts.
			if len(native.Messages) > 0 {
				assertJSON("messages", converted.Messages, native.Messages)
			} else if len(converted.Messages) != 0 {
				t.Fatal("unexpected messages")
			}
			if len(native.Parts) > 0 {
				assertJSON("parts", converted.Parts, native.Parts)
			} else if len(converted.Parts) != 0 {
				t.Fatal("unexpected parts")
			}
		}
	}
}
