package platformconnections

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func openStore(t *testing.T) (*Store, *state.DB) {
	t.Helper()
	db := statetest.Open(t, Schema)
	return New(db), db
}

func input(name string) Input {
	return Input{Kind: "claudecode", Name: name, Configuration: "isolated", Directory: "/accounts/" + name, Enabled: true}
}

func create(t *testing.T, s *Store, name string) Connection {
	t.Helper()
	c, err := s.Create(t.Context(), input(name))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func initialize(t *testing.T, s *Store) {
	t.Helper()
	if changed, err := s.Initialize(t.Context(), Seed{}); err != nil || !changed {
		t.Fatalf("initialize: %v, %v", changed, err)
	}
}

func TestCollectionIdentityAndReopen(t *testing.T) {
	path := statetest.Path(t, Schema)
	db, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db)
	before, err := s.Read(t.Context())
	if err != nil || before.Initialized || before.OnboardingComplete || before.Connections == nil {
		t.Fatalf("absent: %+v %v", before, err)
	}
	if _, err := s.Create(t.Context(), input("before-init")); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("create before initialize: %v", err)
	}
	initialize(t, s)
	personal := create(t, s, "Personal")
	work := create(t, s, "Work")
	if personal.ID == work.ID || personal.ID == personal.Kind || personal.ID == personal.Name || personal.LegacyKind != "" {
		t.Fatalf("identities not distinct: %+v %+v", personal, work)
	}
	if err := s.SetDefault(t.Context(), work.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(t.Context(), work.ID, "  Office  "); err != nil {
		t.Fatal(err)
	}
	work.Name = "Office"
	if err := s.CompleteOnboarding(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	s = New(db)
	got, err := s.Read(t.Context())
	want := Collection{Initialized: true, OnboardingComplete: true, DefaultID: work.ID, Connections: []Connection{personal, work}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened: %+v, want %+v: %v", got, want, err)
	}
	if got, err := s.Default(t.Context()); err != nil || got != work {
		t.Fatalf("default: %+v %v", got, err)
	}
}

func TestDefaultNeverFallsBack(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	personal := create(t, s, "Personal")
	work := create(t, s, "Work")
	if _, err := s.Default(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no explicit default must not pick first: %v", err)
	}
	if err := s.SetDefault(t.Context(), work.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(t.Context(), work.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Default(t.Context()); !errors.Is(err, ErrUnavailable) || got.ID != work.ID {
		t.Fatalf("disabled default fell back: %+v %v", got, err)
	}
	if err := s.SetDefault(t.Context(), work.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("select disabled default: %v", err)
	}
	if got, err := s.Get(t.Context(), personal.ID); err != nil || !got.Enabled {
		t.Fatalf("sibling was disabled: %+v %v", got, err)
	}
	if err := s.SetEnabled(t.Context(), work.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Default(t.Context()); err != nil || got.ID != work.ID {
		t.Fatalf("reenabled default: %+v %v", got, err)
	}
	for range 2 {
		if err := s.Remove(t.Context(), work.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Default(t.Context()); !errors.Is(err, ErrUnavailable) || got.ID != work.ID {
		t.Fatalf("removed default fell back: %+v %v", got, err)
	}
	if err := s.SetEnabled(t.Context(), work.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resurrected removed entry: %v", err)
	}
	if err := s.Rename(t.Context(), work.ID, "replacement"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renamed tombstone: %v", err)
	}
	replacement := create(t, s, "Work")
	if replacement.ID == work.ID {
		t.Fatal("reused removed identity")
	}
	got, err := s.Read(t.Context())
	if err != nil || got.DefaultID != work.ID || !reflect.DeepEqual(got.Connections, []Connection{personal, replacement}) {
		t.Fatalf("removed default preference lost: %+v %v", got, err)
	}
	if err := s.SetDefault(t.Context(), replacement.ID); err != nil {
		t.Fatal(err)
	}
}

func TestValidateInput(t *testing.T) {
	for _, kind := range []string{"opencode", "claudecode", "codex"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := openStore(t)
			initialize(t, s)
			v := input("  Account  ")
			v.Kind, v.Configuration, v.Directory = kind, "existing", "/accounts/one/../two"
			got, err := s.Create(t.Context(), v)
			if err != nil || got.Name != "Account" || got.Directory != filepath.Clean(v.Directory) || got.Kind != kind {
				t.Fatalf("normalized input: %+v %v", got, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*Input)
	}{
		{"kind", func(v *Input) { v.Kind = "remote:claudecode" }},
		{"empty-name", func(v *Input) { v.Name = " \t " }},
		{"long-name", func(v *Input) { v.Name = strings.Repeat("a", 161) }},
		{"control-name", func(v *Input) { v.Name = "a\nb" }},
		{"mode", func(v *Input) { v.Configuration = "shared" }},
		{"empty-path", func(v *Input) { v.Directory = "" }},
		{"relative-path", func(v *Input) { v.Directory = "~/account" }},
		{"control-path", func(v *Input) { v.Directory = "/account\x00" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openStore(t)
			initialize(t, s)
			v := input("test")
			tc.edit(&v)
			if _, err := s.Create(t.Context(), v); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid create: %v", err)
			}
			got, err := s.Read(t.Context())
			if err != nil || len(got.Connections) != 0 {
				t.Fatalf("invalid input wrote row: %+v %v", got, err)
			}
		})
	}
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "test")
	if err := s.Rename(t.Context(), c.ID, " "); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestMissingOperations(t *testing.T) {
	s, _ := openStore(t)
	for name, op := range map[string]func() error{
		"get":        func() error { _, err := s.Get(t.Context(), "missing"); return err },
		"legacy":     func() error { _, err := s.Legacy(t.Context(), "claudecode"); return err },
		"rename":     func() error { return s.Rename(t.Context(), "missing", "name") },
		"enable":     func() error { return s.SetEnabled(t.Context(), "missing", true) },
		"remove":     func() error { return s.Remove(t.Context(), "missing") },
		"default":    func() error { return s.SetDefault(t.Context(), "missing") },
		"onboarding": func() error { return s.CompleteOnboarding(t.Context()) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := op(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
		})
	}
}

func TestConcurrentCreationAtLimit(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	var wg sync.WaitGroup
	errs := make(chan error, maxConnections+8)
	for range maxConnections + 8 {
		wg.Go(func() { _, err := New(db).Create(t.Context(), input("Same name")); errs <- err })
	}
	wg.Wait()
	close(errs)
	successes, limited := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if successes != maxConnections || limited != 8 {
		t.Fatalf("successes=%d limited=%d", successes, limited)
	}
	got, err := s.Read(t.Context())
	if err != nil || len(got.Connections) != maxConnections {
		t.Fatalf("limit: %+v %v", got, err)
	}
	if err := s.Remove(t.Context(), got.Connections[0].ID); err != nil {
		t.Fatal(err)
	}
	create(t, s, "replacement")
}
