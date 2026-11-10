package platformconnections

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func legacySeed() Seed {
	return Seed{ExistingInstallation: true, DefaultKind: "claudecode", Connections: []Input{
		{Kind: "opencode", Name: "OpenCode", Configuration: "existing", Enabled: true},
		{Kind: "claudecode", Name: "Claude Code", Configuration: "existing", Directory: "/accounts/claude", Enabled: true},
	}}
}

func TestInitializeLegacyAndNeverReseed(t *testing.T) {
	s, db := openStore(t)
	seed := legacySeed()
	if changed, err := s.Initialize(t.Context(), seed); err != nil || !changed {
		t.Fatalf("initialize: %v %v", changed, err)
	}
	got, err := s.Read(t.Context())
	if err != nil || !got.Initialized || !got.OnboardingComplete || len(got.Connections) != 2 {
		t.Fatalf("migrated collection: %+v %v", got, err)
	}
	for _, c := range got.Connections {
		mapped, err := s.Legacy(t.Context(), c.Kind)
		if err != nil || mapped != c || c.ID == c.Kind || c.LegacyKind != c.Kind {
			t.Fatalf("legacy mapping: %+v %v", mapped, err)
		}
		if err := s.Remove(t.Context(), c.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got.DefaultID != got.Connections[1].ID {
		t.Fatalf("default mapping: %+v", got)
	}
	for _, seed := range []Seed{seed, {}, {Connections: []Input{{Kind: "invalid"}}}} {
		if changed, err := New(db).Initialize(t.Context(), seed); err != nil || changed {
			t.Fatalf("must ignore later seeds: %v %v", changed, err)
		}
	}
	after, err := s.Read(t.Context())
	if err != nil || !after.Initialized || !after.OnboardingComplete || len(after.Connections) != 0 || after.DefaultID != got.DefaultID {
		t.Fatalf("empty collection was reseeded: %+v %v", after, err)
	}
	if c, err := s.Legacy(t.Context(), "claudecode"); err != nil || !c.Removed || c.ID != got.DefaultID {
		t.Fatalf("removed mapping was lost: %+v %v", c, err)
	}
	newAccount := create(t, s, "Claude Code")
	if newAccount.LegacyKind != "" {
		t.Fatal("new account acquired old mapping")
	}
	if c, err := s.Legacy(t.Context(), "claudecode"); err != nil || c.ID != got.DefaultID {
		t.Fatalf("replacement stole legacy sessions: %+v %v", c, err)
	}
}

func TestEmptyInstallations(t *testing.T) {
	for _, existing := range []bool{false, true} {
		s, _ := openStore(t)
		if _, err := s.Initialize(t.Context(), Seed{ExistingInstallation: existing}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Read(t.Context())
		want := Collection{Initialized: true, OnboardingComplete: existing, Connections: []Connection{}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("empty installation existing=%v: %+v %v", existing, got, err)
		}
		if !existing {
			if err := s.CompleteOnboarding(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err = s.Read(t.Context())
			if err != nil || !got.OnboardingComplete || len(got.Connections) != 0 {
				t.Fatalf("skip onboarding: %+v %v", got, err)
			}
		}
	}
}

func TestInvalidLegacySeedRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Seed)
	}{
		{"fresh-with-connections", func(s *Seed) { s.ExistingInstallation = false }},
		{"duplicate", func(s *Seed) { s.Connections = append(s.Connections, s.Connections[0]) }},
		{"isolated", func(s *Seed) { s.Connections[1].Configuration = "isolated" }},
		{"invalid-kind", func(s *Seed) { s.Connections[1].Kind = "unknown" }},
		{"relative-directory", func(s *Seed) { s.Connections[1].Directory = "relative" }},
		{"default-not-enabled", func(s *Seed) { s.DefaultKind = "codex" }},
		{"too-many", func(s *Seed) { s.Connections = make([]Input, maxConnections+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openStore(t)
			seed := legacySeed()
			tc.edit(&seed)
			if changed, err := s.Initialize(t.Context(), seed); !errors.Is(err, ErrInvalid) || changed {
				t.Fatalf("invalid seed: %v %v", changed, err)
			}
			got, err := s.Read(t.Context())
			if err != nil || got.Initialized || len(got.Connections) != 0 {
				t.Fatalf("partial seed escaped: %+v %v", got, err)
			}
			if _, err := s.Legacy(t.Context(), "opencode"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("partial mapping escaped: %v", err)
			}
			if _, err := s.Initialize(t.Context(), legacySeed()); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
		})
	}
}

func TestInitializePreservesDisabledLegacyDefault(t *testing.T) {
	s, _ := openStore(t)
	seed := legacySeed()
	seed.Connections[1].Enabled = false
	if changed, err := s.Initialize(t.Context(), seed); err != nil || !changed {
		t.Fatalf("migrate disabled account: %v %v", changed, err)
	}
	c, err := s.Default(t.Context())
	if !errors.Is(err, ErrUnavailable) || c.Kind != "claudecode" || c.Enabled || c.LegacyKind != "claudecode" {
		t.Fatalf("disabled preference was replaced: %+v %v", c, err)
	}
	if err := s.SetEnabled(t.Context(), c.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Default(t.Context()); err != nil || got.ID != c.ID {
		t.Fatalf("reenabling changed default identity: %+v %v", got, err)
	}
}

func TestInitializeConcurrentStores(t *testing.T) {
	s, db := openStore(t)
	var wg sync.WaitGroup
	changes := make(chan bool, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			changed, err := New(db).Initialize(t.Context(), legacySeed())
			changes <- changed
			errs <- err
		})
	}
	wg.Wait()
	close(changes)
	close(errs)
	count := 0
	for changed := range changes {
		if changed {
			count++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Read(t.Context())
	if count != 1 || err != nil || len(got.Connections) != 2 {
		t.Fatalf("initialized %d times: %+v %v", count, got, err)
	}
}
