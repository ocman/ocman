package main

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestV2ServerPasswordGeneratesOnceAndPersists(t *testing.T) {
	t.Setenv(opencodeServerPasswordEnv, "")
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	first, err := v2ServerPassword(ctx, st)
	if err != nil || first == "" {
		t.Fatalf("first = %q, %v; want a generated password", first, err)
	}
	second, err := v2ServerPassword(ctx, st)
	if err != nil || second != first {
		t.Fatalf("second = %q, %v; want %q", second, err, first)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// A restarted ocman reads the same password back.
	st, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got, err := v2ServerPassword(ctx, st); err != nil || got != first {
		t.Fatalf("after reopen = %q, %v; want %q", got, err, first)
	}
}
