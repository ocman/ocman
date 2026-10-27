package codexprovider

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// Codex's history app-server rewrites another process's inProgress turn to
// interrupted without supplying a completion timestamp. Item completion alone
// must not close the assistant reply or manufacture an abort error.
func TestSnapshotUnresolvedTurn(t *testing.T) {
	for _, status := range []string{codexappserver.TurnInterrupted, codexappserver.TurnInProgress} {
		t.Run(status, func(t *testing.T) {
			f := &fakeHistory{
				threads: []*codexappserver.Thread{{ID: "thread"}},
				turns:   []codexappserver.Turn{{ID: "turn", Status: status, StartedAt: 1}},
				items: []codexappserver.ItemEntry{
					{TurnID: "turn", Item: codexappserver.Item{Type: "userMessage", ID: "u", Content: []codexappserver.UserInput{{Type: "text", Text: "Review docs"}}}, StartedAtMs: 1000},
					{TurnID: "turn", Item: codexappserver.Item{Type: "agentMessage", ID: "a", Text: "Still working"}, StartedAtMs: 2000, CompletedAtMs: 3000},
				},
			}
			snap, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), "thread")
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.Messages) != 2 {
				t.Fatalf("messages = %+v", snap.Messages)
			}
			info := decodeMap(t, snap.Messages[1].Data)
			if info["error"] != nil || info["finish"] != nil || info["time"].(map[string]any)["completed"] != nil {
				t.Fatalf("active reply was settled by history: %v", info)
			}
			if got := db.SettleSessionStatus(db.TurnRunning, true, snap.InferredStatus); got != db.StatusBusy {
				t.Fatalf("live turn status = %q", got)
			}
			if got := db.SettleSessionStatus(db.TurnUnobserved, false, snap.InferredStatus); got != db.StatusInterrupted {
				t.Fatalf("unobserved unfinished turn status = %q", got)
			}
			// Item completion is still useful and must be preserved on the part.
			if part := decodeMap(t, snap.Parts[1].Data); part["time"].(map[string]any)["end"] != float64(3000) {
				t.Fatalf("completed part = %v", part)
			}
		})
	}
}

func TestHistoryPreservesExplicitInterruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		turn codexappserver.Turn
	}{
		{"timestamp", codexappserver.Turn{ID: "turn", Status: codexappserver.TurnInterrupted, CompletedAt: 4}},
		{"error", codexappserver.Turn{ID: "turn", Status: codexappserver.TurnInterrupted, Error: &codexappserver.TurnError{Message: "cancelled"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Converter{}
			c.AddHistory(&codexappserver.Thread{ID: "thread"}, []codexappserver.Turn{tc.turn}, []codexappserver.ItemEntry{
				{TurnID: "turn", Item: codexappserver.Item{Type: "agentMessage", ID: "a", Text: "Stopped"}, StartedAtMs: 2000, CompletedAtMs: 3000},
			})
			info := decodeMap(t, c.Messages[0].Data)
			if info["error"] == nil || info["error"].(map[string]any)["name"] != "MessageAbortedError" {
				t.Fatalf("explicit interruption lost: %v", info)
			}
		})
	}
}

func TestLiveInterruptionNeedsNoTimestamp(t *testing.T) {
	c := &Converter{SessionID: "thread"}
	c.AddItem(codexappserver.Item{Type: "agentMessage", ID: "a", Text: "Working"}, 1000)
	info := c.Settle(&codexappserver.Turn{ID: "turn", Status: codexappserver.TurnInterrupted})
	if info["error"] == nil || info["error"].(map[string]any)["name"] != "MessageAbortedError" {
		t.Fatalf("live interruption lost: %v", info)
	}
}
