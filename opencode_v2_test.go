package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"

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

func TestPinOpenCodeDB(t *testing.T) {
	absRel, _ := filepath.Abs("rel/oc.db")
	for _, tc := range []struct {
		name, env, path string
		explicit        bool
		wantPath        string
		wantEnv         string
	}{
		{"default path leaves env unset", "", db.DefaultDBPath(), false, db.DefaultDBPath(), ""},
		{"custom path is exported absolute", "", "rel/oc.db", true, absRel, absRel},
		{"user OPENCODE_DB is read when -db is not given", "/set/oc.db", db.DefaultDBPath(), false, "/set/oc.db", "/set/oc.db"},
		{"explicit -db overrides OPENCODE_DB", "/set/oc.db", "/other.db", true, "/other.db", "/other.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENCODE_DB", tc.env)
			if tc.env == "" {
				_ = os.Unsetenv("OPENCODE_DB")
			}
			if got := pinOpenCodeDB(tc.path, tc.explicit); got != tc.wantPath {
				t.Errorf("path = %q, want %q", got, tc.wantPath)
			}
			if got := os.Getenv("OPENCODE_DB"); got != tc.wantEnv {
				t.Errorf("OPENCODE_DB = %q, want %q", got, tc.wantEnv)
			}
		})
	}
}
