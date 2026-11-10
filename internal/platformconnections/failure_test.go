package platformconnections

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCanceledAndClosedDatabase(t *testing.T) {
	for _, closed := range []bool{false, true} {
		s, db := openStore(t)
		initialize(t, s)
		c := create(t, s, "Account")
		ctx, cancel := context.WithCancel(t.Context())
		if closed {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		for name, op := range map[string]func() error{
			"initialize":  func() error { _, err := s.Initialize(ctx, Seed{}); return err },
			"read":        func() error { _, err := s.Read(ctx); return err },
			"get":         func() error { _, err := s.Get(ctx, c.ID); return err },
			"legacy":      func() error { _, err := s.Legacy(ctx, "claudecode"); return err },
			"create":      func() error { _, err := s.Create(ctx, input("new")); return err },
			"rename":      func() error { return s.Rename(ctx, c.ID, "new") },
			"enable":      func() error { return s.SetEnabled(ctx, c.ID, false) },
			"remove":      func() error { return s.Remove(ctx, c.ID) },
			"set-default": func() error { return s.SetDefault(ctx, c.ID) },
			"default":     func() error { _, err := s.Default(ctx); return err },
			"onboarding":  func() error { return s.CompleteOnboarding(ctx) },
		} {
			t.Run(name, func(t *testing.T) {
				if err := op(); err == nil || (!closed && !errors.Is(err, context.Canceled)) {
					t.Fatalf("operation should fail, closed=%v: %v", closed, err)
				}
			})
		}
		cancel()
	}
}

func TestInitializeSQLFailureRollsBack(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER fail_seed BEFORE INSERT ON platform_connection_collection BEGIN SELECT RAISE(ABORT,'seed failure'); END`,
		`CREATE TRIGGER fail_seed BEFORE INSERT ON platform_connection WHEN NEW.kind='claudecode' BEGIN SELECT RAISE(ABORT,'seed failure'); END`,
		`CREATE TRIGGER fail_seed BEFORE UPDATE ON platform_connection_collection BEGIN SELECT RAISE(ABORT,'seed failure'); END`,
	} {
		s, db := openStore(t)
		if _, err := db.SQL().ExecContext(t.Context(), trigger); err != nil {
			t.Fatal(err)
		}
		if changed, err := s.Initialize(t.Context(), legacySeed()); changed || err == nil || !strings.Contains(err.Error(), "seed failure") {
			t.Fatalf("failed initialization: %v %v", changed, err)
		}
		got, err := s.Read(t.Context())
		if err != nil || got.Initialized || len(got.Connections) != 0 {
			t.Fatalf("failed seed leaked: %+v %v", got, err)
		}
		if _, err := s.Legacy(t.Context(), "opencode"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("failed seed leaked a mapping: %v", err)
		}
		if _, err := db.SQL().ExecContext(t.Context(), `DROP TRIGGER fail_seed`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Initialize(t.Context(), legacySeed()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadFailsRatherThanReturningEmpty(t *testing.T) {
	for _, drop := range []string{"platform_connection", "platform_connection_collection"} {
		s, db := openStore(t)
		initialize(t, s)
		if _, err := db.SQL().ExecContext(t.Context(), `DROP TABLE `+drop); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Read(t.Context()); err == nil {
			t.Fatal("broken schema treated as an empty collection")
		}
	}
}
