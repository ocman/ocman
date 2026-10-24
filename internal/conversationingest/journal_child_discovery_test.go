package conversationingest

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestSubagentStopDiscoversChildCreatedAfterBootstrap(t *testing.T) {
	m, src, store := journalModule(t, time.Hour)
	w := m.workers[0]
	w.journaled = true
	w.sweep(t.Context())
	child := historySnapshot("child result")
	child.Session.ID, child.Session.ParentID = "new-child", "session"
	for i := range child.Messages {
		child.Messages[i].SessionID = "new-child"
	}
	for i := range child.Parts {
		child.Parts[i].SessionID = "new-child"
	}
	src.entries = append(src.entries, db.HistoryEntry{ID: "new-child", Version: child.Version})
	src.snapshots["new-child"] = child
	err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session", Type: "SubagentStop"}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.ReadConversation(t.Context(), statev2.Key{InstanceID: w.instance.ID, SessionID: "new-child"}, 10, 0)
	if err != nil || len(detail.Parts) == 0 || detail.Session.ParentID != "session" {
		t.Fatalf("new child not ingested: %+v %v", detail, err)
	}
}
