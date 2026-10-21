package conversationingest

import (
	"errors"
	"testing"
)

func TestAnalyticsKeepsCommittedSourceWhenOneSessionFails(t *testing.T) {
	f := fakeSource("one")
	m, _ := testImporter(t, f)
	w := m.workers[0]
	w.sweep(t.Context())
	f.snapshotErr = errors.New("one session failed")
	m.Notify("one", "session")
	w.sweep(t.Context())
	ids, complete := m.AnalyticsInstances()
	if len(ids) != 1 || ids[0] != sourceKey("one").InstanceID || complete {
		t.Fatalf("failed session hid the committed source: %v complete=%v", ids, complete)
	}
	if _, ready := m.ReadyInstances(); ready {
		t.Fatal("partial analytics must not mark failed ingestion verified")
	}
}

func TestPartialAnalyticsDoesNotBorrowUninitializedOrUnconfiguredSources(t *testing.T) {
	f, g := fakeSource("one"), fakeSource("two")
	m, _ := testImporter(t, f, g)
	if ids, complete := m.AnalyticsInstances(); len(ids) != 0 || complete {
		t.Fatalf("uninitialized sources: %v complete=%v", ids, complete)
	}
	m.workers[0].sweep(t.Context())
	m.setInstance("not-configured", "must-not-leak")
	ids, complete := m.AnalyticsInstances()
	if len(ids) != 1 || ids[0] != sourceKey("one").InstanceID || complete {
		t.Fatalf("partial scope: %v complete=%v", ids, complete)
	}
	m.workers[1].sweep(t.Context())
	if ids, complete := m.AnalyticsInstances(); len(ids) != 2 || !complete {
		t.Fatalf("complete scope: %v complete=%v", ids, complete)
	}
}
