package pluginsmod

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

func TestCompletedUsage(t *testing.T) {
	messages := []db.Message{{ID: "assistant", Data: json.RawMessage(`{"role":"assistant","tokens":{"input":12,"output":8,"reasoning":2,"cache":{"read":3,"write":1}},"cost":0.25}`)}}
	u := completedUsage(messages, "assistant")
	if u == nil || u.PromptTokens != 16 || u.CompletionTokens != 10 || u.CostUSD != 0.25 {
		t.Fatalf("usage: %+v", u)
	}
	if completedUsage(messages, "missing") != nil {
		t.Fatal("invented usage")
	}
	for _, raw := range []string{`{}`, `bad`, `{"tokens":{"input":-1}}`, `{"cost":-1,"tokens":{}}`} {
		messages[0].Data = json.RawMessage(raw)
		if completedUsage(messages, "assistant") != nil {
			t.Fatal("unavailable usage reported as zero")
		}
	}
}

func TestUnavailableCostIsNeverReportedAsZero(t *testing.T) {
	for _, raw := range []string{
		`{"role":"assistant","tokens":{"input":12,"output":8}}`,
		`{"role":"assistant","cost":null,"tokens":{"input":12,"output":8}}`,
	} {
		if u := completedUsage([]db.Message{{ID: "reply", Data: json.RawMessage(raw)}}, "reply"); u != nil {
			t.Fatalf("unknown cost reported as observed zero: %+v", u)
		}
	}
	raw := json.RawMessage(`{"role":"assistant","cost":0,"tokens":{"input":12,"output":8}}`)
	if u := completedUsage([]db.Message{{ID: "reply", Data: raw}}, "reply"); u == nil || u.CostUSD != 0 {
		t.Fatal("explicit zero cost lost availability")
	}
}

func TestV2UnavailableUsageSurvivesConversion(t *testing.T) {
	for _, metadata := range []string{`"tokens":{"input":12,"output":8}`, `"cost":null,"tokens":{"input":12}`, `"cost":0.5`, `"cost":0.5,"tokens":null`} {
		var source map[string]any
		if err := json.Unmarshal([]byte(`{"id":"reply","type":"assistant","time":{"created":1},`+metadata+`}`), &source); err != nil {
			t.Fatal(err)
		}
		converted, ok := ocv2.ConvertMessage("session", source)
		if !ok {
			t.Fatal("invalid fixture")
		}
		raw, _ := json.Marshal(converted.Info)
		var decoded db.MessageData
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(decoded)
		if u := completedUsage([]db.Message{{ID: "reply", Data: raw}}, "reply"); u != nil {
			t.Fatalf("v2 synthesized free usage: %+v", u)
		}
	}
}

func TestUsageOverflowFailsClosed(t *testing.T) {
	if _, ok := sumUsage(math.MaxInt64, 1); ok {
		t.Fatal("token overflow accepted")
	}
	if _, ok := sumUsage(1, -1); ok {
		t.Fatal("negative count accepted")
	}
}

func TestCompletedUsageIncludesWholeTurn(t *testing.T) {
	messages := []db.Message{
		{ID: "reasoning", Data: json.RawMessage(`{"role":"assistant","parentID":"user-a","cost":0.2,"tokens":{"input":10,"output":3}}`)},
		{ID: "reply", Data: json.RawMessage(`{"role":"assistant","parentID":"user-a","cost":0.3,"tokens":{"input":20,"output":4}}`)},
		{ID: "unrelated", Data: json.RawMessage(`{"role":"assistant","parentID":"user-b","cost":100,"tokens":{"input":500}}`)},
	}
	u := completedUsage(messages, "reply")
	if u == nil || u.PromptTokens != 30 || u.CompletionTokens != 7 || u.CostUSD != 0.5 {
		t.Fatalf("turn usage: %+v", u)
	}
	messages[0].Data = json.RawMessage(`{"role":"assistant","parentID":"user-a"}`)
	if completedUsage(messages, "reply") != nil {
		t.Fatal("partial usage treated as known")
	}
}

func TestSessionUsageIsDurableAndIdempotent(t *testing.T) {
	d := openPluginStore(t)
	u := &plugins.SessionUsage{PromptTokens: 12, CompletionTokens: 7, CostUSD: 0.4}
	if _, err := d.AppendPluginSessionCompletion(t.Context(), "plugin", "opencode", "session", "op", "reply", u); err != nil {
		t.Fatal(err)
	}
	u.CostUSD = 99
	if id, err := d.AppendPluginSessionCompletion(t.Context(), "plugin", "opencode", "session", "op", "reply", u); err != nil || id != 0 {
		t.Fatal("duplicate changed receipt")
	}
	restarted := &Store{db: d.db}
	claimed, err := restarted.ClaimPluginSessionCompletions(t.Context(), 10)
	if err != nil || len(claimed) != 1 || claimed[0].Usage == nil || claimed[0].Usage.CostUSD != 0.4 {
		t.Fatalf("durable usage: %+v %v", claimed, err)
	}
	if _, err := d.AppendPluginSessionCompletion(t.Context(), "plugin", "opencode", "other", "bad", "reply", &plugins.SessionUsage{CostUSD: -1}); err == nil {
		t.Fatal("negative cost stored")
	}
}
