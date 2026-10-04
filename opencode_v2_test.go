package main

import (
	"os"
	"path/filepath"
	"strings"
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
	for _, tc := range []struct{ name, env, path, want string }{
		{"default path leaves it unset", "", db.DefaultDBPath(), ""},
		{"custom path is exported absolute", "", "rel/oc.db", "ABS:rel/oc.db"},
		{"explicit env wins", "/set/oc.db", "/other.db", "/set/oc.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENCODE_DB", tc.env)
			if tc.env == "" {
				_ = os.Unsetenv("OPENCODE_DB")
			}
			pinOpenCodeDB(tc.path)
			want := tc.want
			if rel, ok := strings.CutPrefix(want, "ABS:"); ok {
				want, _ = filepath.Abs(rel)
			}
			if got := os.Getenv("OPENCODE_DB"); got != want {
				t.Fatalf("OPENCODE_DB = %q, want %q", got, want)
			}
		})
	}
}
