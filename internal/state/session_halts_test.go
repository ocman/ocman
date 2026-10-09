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
