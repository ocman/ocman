package codextransport

import "testing"

func TestHistoryDeltaReusesReadOnlyReader(t *testing.T) {
	r := newRig(t)
	p := r.platform(t.Context())
	entries, err := p.HistoryInventory(t.Context())
	if err != nil || len(entries) == 0 {
		t.Fatalf("inventory: %+v %v", entries, err)
	}
	first, err := p.history.reader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.history.reader(t.Context())
	if err != nil || first != second {
		t.Fatalf("history reader recreated between calls: %v", err)
	}
	if snapshot, err := p.HistoryDelta(t.Context(), entries[0].ID, ""); err != nil || snapshot == nil || snapshot.Partial {
		t.Fatalf("cached accounting lost complete native snapshot: %+v %v", snapshot, err)
	}
	if _, err := p.HistorySnapshot(t.Context(), entries[0].ID); err != nil {
		t.Fatal(err)
	}
	p.history.close()
	third, err := p.history.reader(t.Context())
	if err != nil || third == first {
		t.Fatalf("new connection retained old reader: %v", err)
	}
}
