package state

import (
	"testing"
	"time"
)

func TestManagedOpencode_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir + "/state.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	const repo = "/src/github.com/example/repo"

	// Absent initially.
	if _, ok, err := d.GetManagedOpencode(t.Context(), repo); err != nil || ok {
		t.Fatalf("initial Get: ok=%v err=%v; want ok=false, no error", ok, err)
	}

	launched := time.Unix(1700000000, 0)
	want := ManagedInstance{
		Endpoint:   "http://127.0.0.1:41235",
		Kind:       "native-tmux",
		RuntimeID:  "sess-name",
		PID:        4242,
		LaunchedAt: launched,
	}
	if err := d.UpsertManagedOpencode(t.Context(), repo, want, launched); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := d.GetManagedOpencode(t.Context(), repo)
	if err != nil || !ok {
		t.Fatalf("Get after upsert: ok=%v err=%v", ok, err)
	}
	if got.Endpoint != want.Endpoint || got.Kind != want.Kind ||
		got.RuntimeID != want.RuntimeID || got.PID != want.PID ||
		!got.LaunchedAt.Equal(launched) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}

	// Upsert replaces the row for the same key.
	relaunched := time.Unix(1700000100, 0)
	want2 := ManagedInstance{Endpoint: "http://127.0.0.1:50000", Kind: "native-tmux", RuntimeID: "sess-2", PID: 99, LaunchedAt: relaunched}
	if err := d.UpsertManagedOpencode(t.Context(), repo, want2, relaunched); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _, err = d.GetManagedOpencode(t.Context(), repo)
	if err != nil {
		t.Fatalf("Get after re-upsert: %v", err)
	}
	if got.Endpoint != want2.Endpoint || got.RuntimeID != want2.RuntimeID {
		t.Fatalf("upsert did not replace: got %+v, want %+v", got, want2)
	}

	// Delete removes it; a second delete is a no-op.
	if err := d.DeleteManagedOpencode(t.Context(), repo); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := d.GetManagedOpencode(t.Context(), repo); err != nil || ok {
		t.Fatalf("Get after delete: ok=%v err=%v; want ok=false", ok, err)
	}
	if err := d.DeleteManagedOpencode(t.Context(), repo); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}
