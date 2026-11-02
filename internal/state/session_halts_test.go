package state

import (
	"path/filepath"
	"testing"
)

func TestSessionHaltReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		session, kind, request string
		at, want               int64
	}{
		{"s", "permission", "p", 100, 100},
		{"s", "permission", "p", 900, 100},
		{"s", "question", "q", 101, 101},
		{"other", "question", "q", 50, 50},
	} {
		got, err := d.RecordSessionHalt(t.Context(), tc.session, tc.kind, tc.request, tc.at)
		if err != nil || got != tc.want {
			t.Fatalf("receipt = %d, %v; want %d", got, err, tc.want)
		}
	}
	if _, err := d.RecordSessionHalt(t.Context(), "", "permission", "p", 100); err == nil {
		t.Fatal("accepted missing session")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.RecordSessionHalt(t.Context(), "s", "question", "q", 999)
	if err != nil || got != 101 {
		t.Fatalf("replayed receipt = %d, %v", got, err)
	}
	halts, err := d.SessionHalts(t.Context())
	if err != nil || len(halts) != 2 || halts["s"] != 101 || halts["other"] != 50 {
		t.Fatalf("halts = %v, %v", halts, err)
	}
}

func TestSessionHaltReceiptsArePruned(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	day := int64(24 * 60 * 60 * 1000)
	record := func(session, request string, at int64) int64 {
		t.Helper()
		got, err := d.RecordSessionHalt(t.Context(), session, "permission", request, at)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	receipts := func() int {
		t.Helper()
		var n int
		if err := d.db.QueryRow(`SELECT count(*) FROM setting WHERE key LIKE 'session.halt.receipt.%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	record("old", "expired", day)
	record("old", "kept", 2*day)
	if got := record("new", "fresh", 31*day+1); got != 31*day+1 {
		t.Fatalf("new receipt = %d", got)
	}
	if n := receipts(); n != 2 {
		t.Fatalf("receipts after prune = %d, want 2", n)
	}
	// A replay of a pruned prompt counts as a new halt; a retained one does not.
	if got := record("old", "kept", 31*day+2); got != 2*day {
		t.Fatalf("retained receipt = %d, want %d", got, 2*day)
	}
	if got := record("old", "expired", 31*day+3); got != 31*day+3 {
		t.Fatalf("pruned receipt = %d, want %d", got, 31*day+3)
	}
	halts, err := d.SessionHalts(t.Context())
	if err != nil || halts["old"] != 31*day+3 || halts["new"] != 31*day+1 {
		t.Fatalf("halts = %v, %v", halts, err)
	}
}
