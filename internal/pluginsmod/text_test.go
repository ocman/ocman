package pluginsmod

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestLatestAssistantText(t *testing.T) {
	messages := []db.Message{
		{ID: "m1", TimeCreated: 100, Data: json.RawMessage(`{"role":"user"}`)},
		{ID: "m3", TimeCreated: 300, Data: json.RawMessage(`{"role":"assistant"}`)},
		{ID: "m2", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)},
		{ID: "broken", TimeCreated: 400, Data: json.RawMessage(`not json`)},
	}
	// Stored parts carry timestamps and may arrive unordered.
	stored := []db.Part{
		{ID: "b", MessageID: "m3", TimeCreated: 320, Data: json.RawMessage(`{"type":"text","text":"world\n"}`)},
		{ID: "a", MessageID: "m3", TimeCreated: 310, Data: json.RawMessage(`{"type":"text","text":"hello"}`)},
		{ID: "c", MessageID: "m3", TimeCreated: 330, Data: json.RawMessage(`{"type":"tool","text":"ignored"}`)},
		{ID: "d", MessageID: "m3", TimeCreated: 340, Data: json.RawMessage(`{"type":"text","text":"   "}`)},
		{ID: "e", MessageID: "m2", TimeCreated: 210, Data: json.RawMessage(`{"type":"text","text":"older"}`)},
	}
	id, text := latestAssistantText(messages, stored)
	if id != "m3" || text != "hello\n\nworld" {
		t.Fatalf("%q %q", id, text)
	}
	// Live parts have no timestamps: arrival order must survive, not ID order.
	live := []db.Part{
		{ID: "zz", MessageID: "m3", Data: json.RawMessage(`{"type":"text","text":"hello"}`)},
		{ID: "aa", MessageID: "m3", Data: json.RawMessage(`{"type":"text","text":"world"}`)},
	}
	if _, text := latestAssistantText(messages, live); text != "hello\n\nworld" {
		t.Fatalf("live part order not preserved: %q", text)
	}
	if id, text := latestAssistantText(messages[:1], stored); id != "" || text != "" {
		t.Fatalf("no assistant message should yield nothing: %q %q", id, text)
	}
	long := []db.Part{{ID: "x", MessageID: "m3", Data: mustPartText(t, strings.Repeat("y", plugins.ConversationMaxTextBytes+10))}}
	if _, text := latestAssistantText(messages, long); len(text) > plugins.ConversationMaxTextBytes {
		t.Fatalf("oversized reply not truncated: %d", len(text))
	}
}

// Assistant answers can contain terminal escapes; the whole reply must not be
// dropped because conversation.v1 forbids them on the wire.
func TestConversationReplyTextSanitizes(t *testing.T) {
	messages := []db.Message{{ID: "m1", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)}}
	parts := []db.Part{{ID: "p", MessageID: "m1", Data: mustPartText(t, "before\x1b[31m\x07after\ttab")}}
	_, text := latestAssistantText(messages, parts)
	if text != "before[31mafter\ttab" {
		t.Fatalf("sanitized text %q", text)
	}
	if (plugins.ConversationReply{AccountID: "T1", ThreadID: "C1:1", Text: text}).Validate() != nil {
		t.Fatal("sanitized text still fails the wire contract")
	}
	trailing := []db.Part{{ID: "p", MessageID: "m1", Data: mustPartText(t, "ok\x1b")}}
	if _, text := latestAssistantText(messages, trailing); text != "ok" {
		t.Fatalf("trailing escape not stripped: %q", text)
	}
}

func mustPartText(t *testing.T, text string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(map[string]string{"type": "text", "text": text})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
