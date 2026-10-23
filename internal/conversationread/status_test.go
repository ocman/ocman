package conversationread

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// A stored turn that stopped while requesting tools is unfinished, not
// waiting for the user. Every stored reader must agree with the summary.
func TestStoredToolCallTurnIsInterruptedInConversationTree(t *testing.T) {
	for _, finish := range []string{"tool-calls", "unknown"} {
		child := statev2.Session{Key: statev2.Key{SessionID: "child"}, ParentID: "root"}
		totals := statev2.ConversationTotals{Role: "assistant", Finish: finish}
		page := &statev2.Detail{Session: statev2.Session{Key: statev2.Key{SessionID: "root"}}, Tree: []statev2.Session{child},
			ConversationTotals: map[string]statev2.ConversationTotals{"child": totals}}
		detail, err := Convert(page, &platforms.ConversationHead{}, "test")
		if err != nil {
			t.Fatal(err)
		}
		summary := db.SettleSessionStatus(db.TurnUnobserved, false, InferredStatus(statev2.SessionSummary{Session: child, Totals: totals}))
		if got := detail.SessionTree[0].Status; got != db.StatusInterrupted || got != summary {
			t.Fatalf("%s: tree status %s, summary %s", finish, got, summary)
		}
	}
}
