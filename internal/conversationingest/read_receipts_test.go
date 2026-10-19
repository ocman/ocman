package conversationingest

import (
	"testing"
	"time"
)

func TestReadReceiptsRequireSuccessfulAccountingAndStableSnapshot(t *testing.T) {
	f := fakeSource("one")
	m, store := testImporter(t, f)
	w := m.workers[0]
	readReceipt := func() string {
		t.Helper()
		hints, err := store.SourceState(t.Context(), sourceKey("one").InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		return hints["read-ready/session"]
	}
	w.sweep(t.Context())
	if readReceipt() == "" {
		t.Fatal("successful import has no validated read receipt")
	}
	f.changingReads = 100
	m.Notify("one", "session")
	w.sweep(t.Context())
	if readReceipt() != "" {
		t.Fatal("deferred snapshot retained read approval")
	}
	f.changingReads = 0
	w.sweep(t.Context())
	if readReceipt() == "" {
		t.Fatal("stable retry did not restore approval")
	}
	batch, _, err := Normalize(sourceKey("one"), historySnapshot("one"))
	if err != nil {
		t.Fatal(err)
	}
	batch.ExpectedRevision = 1
	batch.Messages[0].Cost = 42
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	w.verifiedAt = time.Time{}
	w.sweep(t.Context())
	if readReceipt() != "" {
		t.Fatal("accounting mismatch retained read approval")
	}
}

func TestOlderReadFormatIsRefreshedWithoutSourceHintChange(t *testing.T) {
	f := fakeSource("one")
	m, store := testImporter(t, f)
	w := m.workers[0]
	w.sweep(t.Context())
	batch, _, err := Normalize(sourceKey("one"), historySnapshot("one"))
	if err != nil {
		t.Fatal(err)
	}
	batch.ExpectedRevision = 1
	batch.Session.ReadFormat = 0
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	p, err := w.prepare(t.Context(), nil)
	if err != nil || len(p.reads) != 1 {
		t.Fatal("old metadata was skipped", p, err)
	}
	w.sweep(t.Context())
	page, err := store.ReadConversation(t.Context(), sourceKey("one"), 30, 0)
	if err != nil || page.Session.ReadFormat == 0 {
		t.Fatal("metadata refresh did not repair snapshot", page, err)
	}
}
