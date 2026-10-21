package owned

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestDeletedCanonicalConversationStaysReadableFromRetainedHistory(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	snapshot := &db.HistorySnapshot{Version: "v1", Session: db.Session{ID: id, Title: "retained", Directory: "/repo"}, Messages: []db.Message{{ID: "message", SessionID: id, Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)}}, Parts: []db.Part{{ID: "part", MessageID: "message", SessionID: id, Data: json.RawMessage(`{"type":"text","text":"retained answer"}`)}}}
	batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "source", SessionID: id}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	batch.Session.SourceDeleted = true
	batch.ExpectedRevision = 1
	if _, err := a.history.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	// The source no longer lists the session; its native transcript is gone.
	p.rows = nil
	detail, err := a.Session(t.Context(), id, 30, 0)
	if err != nil || detail == nil || detail.ConversationSource != "statev2" || detail.Messages[0].ID != "message" || detail.Parts[0].ID != "part" || detail.Session.ID != id {
		t.Fatalf("retained conversation unreadable: %+v %v", detail, err)
	}
	if rows, err := a.Sessions(t.Context(), "", 0); err != nil || len(rows) != 0 {
		t.Fatalf("source-deleted session stayed in the browsing list: %+v %v", rows, err)
	}
}
