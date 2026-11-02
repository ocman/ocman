package conversationingest

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/statev2"
)

func TestPeriodicInventoryRunsDuringContinuousKnownActivity(t *testing.T) {
	f := bulkHistory(3)
	m, store := testImporter(t, f)
	w := m.workers[0]
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	addSession(f, "unrelated-new", "v1", "discovered")
	f.entries = append(f.entries[:1], f.entries[2:]...) // session-1 disappeared
	m.deps.PollInterval = 5 * time.Millisecond
	f.afterSnapshot = func() { m.Observe("bulk", "session-0") }
	m.Observe("bulk", "session-0")
	stop := runModule(t, m)
	defer stop()
	eventually(t, "known-session activity starved periodic discovery or removal detection", func() bool {
		key := statev2.Key{InstanceID: w.instance.ID, SessionID: "unrelated-new"}
		created, err := store.SessionState(t.Context(), key)
		if err != nil || created.Revision == 0 {
			return false
		}
		key.SessionID = "session-1"
		removed, err := store.SessionState(t.Context(), key)
		return err == nil && removed.SourceDeleted
	})
}
