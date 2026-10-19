package conversationread

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type fixedPricing float64

func (p fixedPricing) CalcCost(_ string, in, out, read, write int64) float64 {
	return float64(in+out+read+write) * float64(p)
}

func TestConversationTotalsKeepDynamicAndPaidCostsSeparate(t *testing.T) {
	s, p, store, batch := readFixture(t)
	batch.ExpectedRevision = 1
	batch.Messages = append(batch.Messages, statev2.Message{ID: "paid", Role: "assistant", Position: 3, ConversationVisible: true, Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"m","cost":9,"tokens":{"input":4,"output":1},"finish":"stop","time":{"created":7,"completed":10}}`)})
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	p.head.Pricing = fixedPricing(2)
	d, _, err := s.ReadConversation(t.Context(), p, "s", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The first assistant has no model, so only the paid request is estimated.
	if d.Session.TotalCost != 9 || d.Session.TotalEffectiveCost != 9 || d.Session.TotalEstCost != 10 || d.Session.TotalInputTokens != 14 || d.Session.TotalOutputTokens != 6 || d.Session.MessageCount != 1 || d.Session.ActiveDurationMs != 6 {
		t.Fatalf("incorrect totals: %+v", d.Session)
	}
	p.head.Pricing = fixedPricing(3)
	d, _, err = s.ReadConversation(t.Context(), p, "s", 1, 0)
	if err != nil || d.Session.TotalEstCost != 15 || d.Session.TotalEffectiveCost != 9 {
		t.Fatal("pricing was frozen", d, err)
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
