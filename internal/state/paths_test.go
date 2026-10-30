package state

import (
	"path/filepath"
	"testing"
)

func TestStatePathOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "isolated", "state.db")
	t.Setenv("OCMAN_STATE_DB", path)
	if got := DefaultDBPath(); got != path {
		t.Fatalf("state path = %q, want %q", got, path)
	}
	if got := DefaultDataDir(); got != filepath.Dir(path) {
		t.Fatalf("data directory = %q, want %q", got, filepath.Dir(path))
	}
	t.Setenv("OCMAN_STATE_DB", "")
	if got := DefaultDBPath(); got == path || filepath.Base(got) != "state.db" {
		t.Fatalf("default state path = %q", got)
	}
}
