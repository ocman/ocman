package statev2

import (
	"encoding/json"
	"testing"
)

func TestConversationPageUsesLatestVisibleSnapshotAndNewestOffsets(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	b.Complete = true
	b.Session.ReadFormat = ConversationReadFormat
	b.Messages = []Message{{ID: "a", Role: "user", Position: 0, ConversationVisible: true, Extensions: json.RawMessage(`{"role":"user"}`)},
		{ID: "opaque", Role: "unknown", Position: 1},
		{ID: "b", Role: "assistant", Position: 2, ConversationVisible: true, Extensions: json.RawMessage(`{"role":"assistant","finish":"stop"}`)}}
	b.Parts = []Part{{ID: "text", MessageID: "b", Kind: "text", Text: "retained", CreatedAt: 123, TextPresent: true}}
	commit(t, d, b)
	page, err := d.ReadConversation(t.Context(), b.Session.Key, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalMessages != 2 || len(page.Messages) != 1 || page.Messages[0].ID != "b" || page.Parts[0].CreatedAt != 123 || !page.Parts[0].TextPresent {
		t.Fatalf("newest page: %+v", page)
	}
	page, err = d.ReadConversation(t.Context(), b.Session.Key, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Messages[0].ID != "a" || len(page.Parts) != 0 {
		t.Fatalf("older page: %+v", page)
	}
	// Retained audit records must not reappear in the current conversation.
	b.ExpectedRevision = 1
	b.Messages = b.Messages[:1]
	b.Parts = nil
	commit(t, d, b)
	page, err = d.ReadConversation(t.Context(), b.Session.Key, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalMessages != 1 || page.Messages[0].ID != "a" || len(page.Parts) != 0 {
		t.Fatalf("removed content resurfaced: %+v", page)
	}
	if audit := read(t, d, b.Session.Key); audit.TotalMessages != 3 {
		t.Fatal("audit retention changed", audit)
	}
}

// Composer defaults come from the newest root session; a subagent's agent
// is never offered, whatever its title says.
func TestConversationDefaultsSkipSubagents(t *testing.T) {
	d := openTestDB(t)
	session := func(id string, updated int64, subagent bool, agent string) Batch {
		b := fixture("first")
		b.Checkpoint, b.Complete = nil, true
		b.Session.SessionID, b.Session.Title, b.Session.UpdatedAt, b.Session.Subagent = id, "Find the config loader", updated, subagent
		b.Session.ReadFormat = ConversationReadFormat
		b.Messages = []Message{{ID: id + "-a", Role: "assistant", Agent: agent, CreatedAt: updated, CompletedAt: updated,
			ConversationVisible: true, Extensions: json.RawMessage(`{"role":"assistant","agent":"` + agent + `"}`)}}
		b.Parts = nil
		return b
	}
	commit(t, d, session("root", 100, false, "build"))
	commit(t, d, session("helper", 200, true, "explore"))
	commit(t, d, session("current", 300, false, ""))
	page, err := d.ReadConversation(t.Context(), Key{InstanceID: "first", SessionID: "current"}, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Conversation.DefaultAgent != "build" {
		t.Fatalf("default agent = %q, want the root session's", page.Conversation.DefaultAgent)
	}
}
