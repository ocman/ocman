package conversationingest

import (
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/historyjournal"
)

func TestPollingActivityDoesNotInventoryUnrelatedSessions(t *testing.T) {
	f := bulkHistory(3)
	m, _ := testImporter(t, f)
	w := m.workers[0]
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.inventoryErr = errors.New("single-session activity must not inventory the source")
	m.Observe("bulk", "session-0")
	before := f.reads
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.reads-before != 1 {
		t.Fatalf("targeted activity read %d sessions", f.reads-before)
	}
	// Periodic verification remains a complete source pass.
	w.verifiedAt = time.Time{}
	if err := w.sweepChecked(t.Context()); err == nil {
		t.Fatal("verification skipped authoritative inventory")
	}
}

func TestUnknownJournalHookDiscoversWithoutRepairingUnchangedHistory(t *testing.T) {
	f := bulkHistory(3)
	m, _ := testImporter(t, f)
	w := m.workers[0]
	w.journaled = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := f.reads
	if err := w.refreshJournal(t.Context(), []historyjournal.Record{{Type: "Stop"}}); err != nil {
		t.Fatal(err)
	}
	if f.reads != before {
		t.Fatalf("unresolved hook reread %d unchanged sessions", f.reads-before)
	}
}

func TestPollingVerificationBypassesIncrementalSourceReads(t *testing.T) {
	f := fakeSource("delta")
	m, store := testImporter(t, f)
	w := m.workers[0]
	delta := &deltaHistory{fakeHistory: f, snapshot: historySnapshot("cached update")}
	w.source.Reader = delta
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.Observe("delta", "session")
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if text, _ := importText(t, store, sourceKey("delta")); text != "cached update" {
		t.Fatalf("incremental path unused: %q", text)
	}
	w.verifiedAt = time.Time{}
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if text, _ := importText(t, store, sourceKey("delta")); text != "delta" || len(delta.requested) != 1 {
		t.Fatalf("verification trusted incremental state: text=%q requests=%v", text, delta.requested)
	}
}
