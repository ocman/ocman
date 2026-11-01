package claudecode

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/conversationread"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/pricing"
	"github.com/ocman/ocman/internal/statev2"
)

func TestStoredReadsSupplyPricingConfig(t *testing.T) {
	var integration any = &claudePlatform{}
	config, ok := integration.(platforms.StoredReadConfigurer)
	if !ok {
		t.Fatal("Claude stored reads do not supply pricing configuration")
	}
	if head := config.StoredReadConfig("/repo"); head == nil || !head.DirectoryDefaults {
		t.Fatal("Claude stored reads lost directory defaults")
	}
}

func TestStoredClaudeCostsUseAPIRates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"claude-sonnet-4-5":{"mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000015,"cache_read_input_token_cost":0.0000003,"cache_creation_input_token_cost":0.00000375}}`))
	}))
	defer server.Close()
	table := pricing.New(server.Client(), server.URL)
	if err := table.LoadCtx(t.Context()); err != nil {
		t.Fatal(err)
	}
	page := &statev2.Detail{
		Session: statev2.Session{Key: statev2.Key{SessionID: "root"}},
		Tree:    []statev2.Session{{Key: statev2.Key{SessionID: "root"}}, {Key: statev2.Key{SessionID: "child"}, ParentID: "root"}},
		ConversationTotals: map[string]statev2.ConversationTotals{
			"root":  {Models: []statev2.ConversationModelTotal{{Provider: "anthropic", Model: "claude-sonnet-4-5", Input: 1000, Output: 100, CacheRead: 2000, CacheWrite: 400}}},
			"child": {Models: []statev2.ConversationModelTotal{{Provider: "anthropic", Model: "claude-sonnet-4-5", Input: 1000}}},
		},
	}
	for _, tc := range []struct {
		name    string
		pricing CostCalculator
		want    float64
	}{
		{"priced", table, 0.0066},
		{"unavailable", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &claudePlatform{pricing: tc.pricing}
			detail, err := conversationread.Convert(page, p.StoredReadConfig("/repo"), "claudecode")
			if err != nil {
				t.Fatal(err)
			}
			if got := detail.Session; got.TotalCost != 0 || !closeCost(got.TotalEstCost, tc.want) || !closeCost(got.TotalEffectiveCost, tc.want) {
				t.Fatalf("subscription estimate missing or treated as billed: %+v", got)
			}
			for _, row := range detail.SessionTree {
				want := tc.want
				if row.ID == "child" && tc.pricing != nil {
					want = 0.003
				}
				if !closeCost(row.TotalEstCost, want) || !closeCost(row.TotalEffectiveCost, want) {
					t.Fatalf("tree estimate missing: %+v", row)
				}
			}
		})
	}
}

func closeCost(got, want float64) bool {
	return got-want < 1e-9 && want-got < 1e-9
}
