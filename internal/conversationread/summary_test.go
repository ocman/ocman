package conversationread

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestPersistedSummaryKeepsStaticInferenceSeparateFromLiveState(t *testing.T) {
	s := statev2.SessionSummary{Session: statev2.Session{Key: statev2.Key{InstanceID: "i", SessionID: "s"}, Title: "stored", Directory: "/repo", Extensions: json.RawMessage(`{"InferredStatus":"busy","SummaryFiles":2}`)}, Totals: statev2.ConversationTotals{Role: "user", Messages: 1, Input: 7, Output: 3, Cost: 0.5}}
	row := Summary(s, "test", &platforms.ConversationHead{})
	if row.ID != "s" || row.Status != db.StatusInterrupted || row.TotalCost != 0.5 || row.MessageCount != 1 || row.SummaryFiles == nil || *row.SummaryFiles != 2 {
		t.Fatal(row)
	}
	if metadata := Metadata(s.Session, "test"); metadata.Title != "stored" || metadata.TotalCost != 0 {
		t.Fatal(metadata)
	}
	for _, tc := range []struct {
		extra, role, finish string
		want                db.SessionStatus
	}{
		{`{}`, "assistant", "tool-calls", db.StatusBusy}, {`{}`, "assistant", "unknown", db.StatusBusy}, {`{}`, "assistant", "stop", db.StatusWaiting}, {`{"InferredStatus":"unrecognized"}`, "assistant", "stop", db.StatusWaiting}, {`{"InferredStatus":"done"}`, "user", "", db.StatusDone}, {`{"InferredStatus":"error"}`, "user", "", db.StatusError}, {`{"InferredStatus":"interrupted"}`, "user", "", db.StatusInterrupted},
	} {
		s.Extensions = json.RawMessage(tc.extra)
		s.Totals.Role, s.Totals.Finish = tc.role, tc.finish
		if got := InferredStatus(s); got != tc.want {
			t.Fatalf("%+v => %s", tc, got)
		}
	}
}
