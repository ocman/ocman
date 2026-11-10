package platformconnections

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestRuntimeStoreConnectionScope(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	a, b := create(t, s, "A"), create(t, s, "B")
	left, err := NewRuntimeStore(t.Context(), db, a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewRuntimeStore(t.Context(), db, b)
	if err != nil {
		t.Fatal(err)
	}
	for i, store := range []*RuntimeStore{left, right} {
		inst := state.ManagedInstance{Endpoint: []string{"unix:///a", "unix:///b"}[i], Kind: "provider-tmux", RuntimeID: "native", PID: 123, LaunchedAt: time.Unix(1234, 0)}
		if err := store.UpsertManagedProvider(t.Context(), "claudecode", "/project", inst); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.GetManagedProvider(t.Context(), "claudecode", "/project")
		if err != nil || !ok || !reflect.DeepEqual(got, inst) {
			t.Fatalf("round trip: %+v %v %v", got, ok, err)
		}
	}
	if _, ok, err := db.GetManagedProvider(t.Context(), "claudecode", "/project"); err != nil || ok {
		t.Fatalf("legacy polluted: %v %v", ok, err)
	}
	if err := left.DeleteManagedProvider(t.Context(), "claudecode", "/project"); err != nil {
		t.Fatal(err)
	}
	if roots, err := left.ListManagedProviders(t.Context(), "claudecode"); err != nil || len(roots) != 0 {
		t.Fatalf("deleted: %v %v", roots, err)
	}
	reopened, err := NewRuntimeStore(t.Context(), db, b)
	if err != nil {
		t.Fatal(err)
	}
	if roots, err := reopened.ListManagedProviders(t.Context(), "claudecode"); err != nil || !reflect.DeepEqual(roots, []string{"/project"}) {
		t.Fatalf("sibling: %v %v", roots, err)
	}
	if got, ok, err := reopened.GetManagedProvider(t.Context(), "claudecode", "/project"); err != nil || !ok || got.Endpoint != "unix:///b" {
		t.Fatalf("restart: %+v %v %v", got, ok, err)
	}
	if _, _, err := left.GetManagedProvider(t.Context(), "codex", "/project"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := left.UpsertManagedProvider(t.Context(), "codex", "/project", state.ManagedInstance{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := left.DeleteManagedProvider(t.Context(), "codex", "/project"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := left.ListManagedProviders(t.Context(), "codex"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestRuntimeStoreRequiresExactAvailableBinding(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	c := create(t, s, "A")
	if _, err := NewRuntimeStore(t.Context(), nil, c); err == nil {
		t.Fatal("nil owner accepted")
	}
	changed := c
	changed.Directory = "/different"
	if _, err := NewRuntimeStore(t.Context(), db, changed); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	changed = c
	changed.Kind = "codex"
	if _, err := NewRuntimeStore(t.Context(), db, changed); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	changed = c
	changed.ID = "missing"
	if _, err := NewRuntimeStore(t.Context(), db, changed); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SetEnabled(t.Context(), c.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntimeStore(t.Context(), db, c); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestRuntimeStoreSurvivesDatabaseReopen(t *testing.T) {
	path := statetest.Path(t, Schema)
	db, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db)
	initialize(t, s)
	c := create(t, s, "restarted")
	runtime, err := NewRuntimeStore(t.Context(), db, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.UpsertManagedProvider(t.Context(), c.Kind, "/repo", state.ManagedInstance{Endpoint: "unix:///persisted"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	runtime, err = NewRuntimeStore(t.Context(), db, c)
	if err != nil {
		t.Fatal(err)
	}
	got, exists, err := runtime.GetManagedProvider(t.Context(), c.Kind, "/repo")
	if err != nil || !exists || got.Endpoint != "unix:///persisted" {
		t.Fatalf("reopen: %+v %v %v", got, exists, err)
	}
}
