package claudeprovider

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFamilyDiscoveryDoesNotRefreshAnotherProject(t *testing.T) {
	config := fixture(t)
	x := openSynced(t, config)
	h := &HistoryReader{index: x}
	other := filepath.Join(config, "projects", "-work-other", otherID+".jsonl")
	st, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, st.ModTime().Add(1), st.ModTime().Add(1)); err != nil {
		t.Fatal(err)
	}
	parent, err := x.get(t.Context(), mainID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := x.get(t.Context(), childID)
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(filepath.Dir(child.Path), "agent-new.jsonl")
	raw, err := os.ReadFile(child.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := x.parsed.Load()
	rows, err := h.Family(t.Context(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || x.parsed.Load()-before != 1 {
		t.Fatalf("family discovery rows=%d reparses=%d", len(rows), x.parsed.Load()-before)
	}
	if err := os.Remove(newPath); err != nil {
		t.Fatal(err)
	}
	rows, err = h.Family(t.Context(), childID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("removed child still indexed: %+v %v", rows, err)
	}
	if _, err := h.Family(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown family: %v", err)
	}
}
