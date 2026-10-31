package pluginsmod

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestToolSessionBudgetRequiresCompleteObservedCost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []db.Message
		total    int
		allowed  bool
	}{
		{"under cap", []db.Message{{Data: json.RawMessage(`{"role":"assistant","cost":0.1}`)}}, 1, true},
		{"whole tool loop over cap", []db.Message{{Data: json.RawMessage(`{"role":"assistant","cost":0.3}`)}, {Data: json.RawMessage(`{"role":"assistant","cost":0.3}`)}}, 2, false},
		{"exact cap", []db.Message{{Data: json.RawMessage(`{"role":"assistant","cost":0.5}`)}}, 1, false},
		{"unknown cost", []db.Message{{Data: json.RawMessage(`{"role":"assistant"}`)}}, 1, false},
		{"unavailable metadata", []db.Message{{Data: json.RawMessage(`{"role":"assistant","cost":0,"usageAvailable":false}`)}}, 1, false},
		{"negative cost", []db.Message{{Data: json.RawMessage(`{"role":"assistant","cost":-1}`)}}, 1, false},
		{"truncated history", []db.Message{{Data: json.RawMessage(`{"role":"assistant","cost":0.1}`)}}, 20, false},
		{"no assistant usage", []db.Message{{Data: json.RawMessage(`{"role":"user"}`)}}, 1, false},
		{"invalid metadata", []db.Message{{Data: json.RawMessage(`invalid`)}}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := toolModule(t)
			p := &followupPlatform{detail: &platforms.SessionDetail{Messages: tc.messages, TotalMessages: tc.total}}
			m.deps.Adapter = func(context.Context, string, string) (platforms.Platform, bool) { return p, true }
			b := openTools(t, m, "inventory", "session-a", "run-a", "lookup", 2)
			b.params.MaxCostUSD = 0.5
			if got := toolBudgetAllows(b, m.toolSessionCost(t.Context(), b)); got != tc.allowed {
				t.Fatalf("allowed=%v, want %v", got, tc.allowed)
			}
		})
	}
}
