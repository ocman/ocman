package conversationread

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/conversationingest"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func readFixture(t *testing.T) (*statev2.DB, statev2.Batch) {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "/source"}); err != nil {
		t.Fatal(err)
	}
	snapshot := &db.HistorySnapshot{Session: db.Session{ID: "s", Title: "Stored", Directory: "/repo", TimeCreated: 1, TimeUpdated: 6},
		Messages: []db.Message{{ID: "u", SessionID: "s", TimeCreated: 1, Data: json.RawMessage(`{"role":"user","model":{"providerID":"p","modelID":"m"}}`)},
			{ID: "opaque", SessionID: "s", TimeCreated: 2, Data: json.RawMessage(`{"role":"unknown"}`)},
			{ID: "a", SessionID: "s", TimeCreated: 3, Data: json.RawMessage(`{"role":"assistant","finish":"stop","tokens":{"input":10,"output":5,"reasoning":3,"cache":{"read":2,"write":1}},"time":{"created":3,"completed":6}}`)}},
		Parts: []db.Part{{ID: "text", MessageID: "a", SessionID: "s", TimeCreated: 4, Data: json.RawMessage(`{"type":"text","text":"","extra":1234567890123456789}`)},
			{ID: "tool", MessageID: "a", SessionID: "s", TimeCreated: 5, Data: json.RawMessage(`{"type":"tool","tool":"bash","state":{"input":{"cmd":"ok"},"output":"done"}}`)}},
		NativeMessages: map[string]json.RawMessage{"opaque": json.RawMessage(`{"kind":"system"}`), "a": json.RawMessage(`{"nativeOnly":true}`)}}
	batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "source", SessionID: "s"}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	return store, batch
}

func readStored(t *testing.T, store *statev2.DB, head *platforms.ConversationHead, limit int) *platforms.SessionDetail {
	t.Helper()
	page, err := store.ReadConversation(t.Context(), statev2.Key{InstanceID: "source", SessionID: "s"}, limit, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Convert(page, head, "test")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStoredConversationRestoresWireFields(t *testing.T) {
	store, _ := readFixture(t)
	d := readStored(t, store, &platforms.ConversationHead{DirectoryDefaults: true}, 1)
	if d.ConversationSource != "statev2" || d.TotalMessages != 2 || len(d.Messages) != 1 || d.Messages[0].ID != "a" {
		t.Fatalf("did not select stored newest page: %+v", d)
	}
	if d.ContextTokenCount != 21 || d.DefaultModel != "p/m" || len(d.Parts) != 2 || d.Parts[0].TimeCreated != 4 || d.Parts[1].TimeCreated != 5 {
		t.Fatalf("wire metadata changed: %+v", d)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(d.Messages[0].Data, &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := metadata["native"]; ok {
		t.Fatal("native envelope leaked into v1 metadata")
	}
	var text, tool map[string]json.RawMessage
	_ = json.Unmarshal(d.Parts[0].Data, &text)
	_ = json.Unmarshal(d.Parts[1].Data, &tool)
	if string(text["text"]) != `""` || string(text["extra"]) != "1234567890123456789" {
		t.Fatal("empty text or integer precision lost", text)
	}
	if _, ok := tool["text"]; ok {
		t.Fatal("fabricated text field on tool part")
	}
	// A settled stored read never reports a running turn on its own.
	if d.Session.Status == db.StatusBusy {
		t.Fatalf("stored inference claimed a running turn: %s", d.Session.Status)
	}
}

type fixedPricing float64

func (p fixedPricing) CalcCost(_ string, in, out, read, write int64) float64 {
	return float64(in+out+read+write) * float64(p)
}

func TestConversationTotalsKeepDynamicAndPaidCostsSeparate(t *testing.T) {
	store, batch := readFixture(t)
	batch.ExpectedRevision = 1
	batch.Messages = append(batch.Messages, statev2.Message{ID: "paid", Role: "assistant", Position: 3, ConversationVisible: true, Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"m","cost":9,"tokens":{"input":4,"output":1},"finish":"stop","time":{"created":7,"completed":10}}`)})
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	d := readStored(t, store, &platforms.ConversationHead{Pricing: fixedPricing(2)}, 1)
	// The first assistant has no model, so only the paid request is estimated.
	if d.Session.TotalCost != 9 || d.Session.TotalEffectiveCost != 9 || d.Session.TotalEstCost != 10 || d.Session.TotalInputTokens != 14 || d.Session.TotalOutputTokens != 6 || d.Session.MessageCount != 1 || d.Session.ActiveDurationMs != 6 {
		t.Fatalf("incorrect totals: %+v", d.Session)
	}
	d = readStored(t, store, &platforms.ConversationHead{Pricing: fixedPricing(3)}, 1)
	if d.Session.TotalEstCost != 15 || d.Session.TotalEffectiveCost != 9 {
		t.Fatal("pricing was frozen", d)
	}
	total := statev2.ConversationTotals{Cost: 5, Models: []statev2.ConversationModelTotal{{Provider: "p", Model: "paid", Input: 2, Paid: 5}, {Model: "subscription", Output: 3}, {Input: 100}}}
	row := db.Session{TotalEstCost: 999}
	applyTotals(&row, total, &platforms.ConversationHead{Pricing: fixedPricing(2)})
	if row.TotalEstCost != 10 || row.TotalEffectiveCost != 11 {
		t.Fatalf("paid/unpaid accounting: %+v", row)
	}
}

func TestConvertRejectsMalformedPayloadAndPreservesNullText(t *testing.T) {
	for _, kind := range []string{"message", "part"} {
		t.Run(kind, func(t *testing.T) {
			page := &statev2.Detail{}
			if kind == "message" {
				page.Messages = []statev2.Message{{Extensions: json.RawMessage(`{`)}}
			} else {
				page.Parts = []statev2.Part{{Extensions: json.RawMessage(`{`)}}
			}
			if _, err := Convert(page, &platforms.ConversationHead{}, "test"); err == nil {
				t.Fatal("malformed payload accepted")
			}
		})
	}
	page := &statev2.Detail{Session: statev2.Session{Key: statev2.Key{SessionID: "s"}, Extensions: json.RawMessage(`{"SummaryFiles":2}`)}, Parts: []statev2.Part{{Kind: "text", Extensions: json.RawMessage(`null`)}, {Kind: "tool", Extensions: json.RawMessage(`{"text":null}`)}}}
	d, err := Convert(page, &platforms.ConversationHead{}, "test")
	if err != nil || d.Session.SummaryFiles == nil || *d.Session.SummaryFiles != 2 {
		t.Fatal(d, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(d.Parts[1].Data, &fields); err != nil || string(fields["text"]) != "null" {
		t.Fatal("null text changed", fields, err)
	}
}
