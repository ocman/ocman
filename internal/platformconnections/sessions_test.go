package platformconnections

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
)

func sessionID() string { return "ocm_" + uuid.NewString() }

func TestSessionBindingIsImmutableAndSurvivesRemoval(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	a, b, id := create(t, s, "A"), create(t, s, "B"), sessionID()
	if err := s.BindSession(t.Context(), id, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSession(t.Context(), id, a.ID); err != nil {
		t.Fatal("idempotent binding failed", err)
	}
	if err := s.BindSession(t.Context(), id, b.ID); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("retargeted an existing session: %v", err)
	}
	if err := s.Remove(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionConnection(t.Context(), id)
	if err != nil || got != a.ID {
		t.Fatalf("removal lost session binding: %q %v", got, err)
	}
	if err := s.BindSession(t.Context(), id, a.ID); err != nil {
		t.Fatal("retained binding was not idempotent", err)
	}
}

func TestSessionAdmissionPinsDefaultAcrossRetries(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	a, b, id := create(t, s, "A"), create(t, s, "B"), sessionID()
	r := NewRouter(s)
	for _, c := range []Connection{a, b} {
		if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDefault(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSession(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session used current default: %v", err)
	}
	first, err := r.AdmitSession(t.Context(), id, Reference{})
	if err != nil || first.Connection.ID != a.ID {
		t.Fatalf("initial admission: %+v %v", first, err)
	}
	if err := s.SetDefault(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := r.AdmitSession(t.Context(), id, Reference{})
	if err != nil || retry.Connection.ID != a.ID {
		t.Fatalf("retry followed changed default: %+v %v", retry, err)
	}
	if _, err := r.AdmitSession(t.Context(), id, Reference{ConnectionID: b.ID}); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("explicit retry retargeted account: %v", err)
	}
	if err := s.SetEnabled(t.Context(), a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSession(t.Context(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled account fell back on existing session: %v", err)
	}
	if _, err := r.AdmitSession(t.Context(), id, Reference{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled account fell back on retry: %v", err)
	}
}

func TestSessionBindingValidationAndFailure(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	for _, id := range []string{"", "native", "cnx_" + uuid.NewString()} {
		if err := s.BindSession(t.Context(), id, c.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid session identity bound: %v", err)
		}
		if _, err := r.AdmitSession(t.Context(), id, Reference{}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.BindSession(t.Context(), sessionID(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SetEnabled(t.Context(), c.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSession(t.Context(), sessionID(), c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := NewRouter(nil).ResolveSession(t.Context(), sessionID()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := NewRouter(nil).AdmitSession(t.Context(), sessionID(), Reference{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.BindSession(ctx, sessionID(), c.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSession(t.Context(), sessionID()); err == nil {
		t.Fatal("closed store accepted resolution")
	}
	if err := s.BindSession(t.Context(), sessionID(), c.ID); err == nil {
		t.Fatal("closed store accepted binding")
	}
}

func TestConcurrentSessionBindingsChooseOnlyOneAccount(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	a, b, id := create(t, s, "A"), create(t, s, "B"), sessionID()
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			c := a
			if i%2 != 0 {
				c = b
			}
			if err := s.BindSession(t.Context(), id, c.ID); err != nil && !errors.Is(err, ErrSessionConflict) {
				t.Errorf("competing bindings: %v", err)
			}
		})
	}
	wg.Wait()
	got, err := s.SessionConnection(t.Context(), id)
	if err != nil || (got != a.ID && got != b.ID) {
		t.Fatalf("no durable winner: %q %v", got, err)
	}
}

func TestSessionSchemaUpgradeAndReopen(t *testing.T) {
	previous := Schema
	previous.Tables, previous.Migrations = previous.Tables[:3], previous.Migrations[:2]
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path, previous)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db)
	initialize(t, s)
	// Seed the historical schema directly, not through today's Create contract.
	c := Connection{ID: "cnx_" + uuid.NewString(), Kind: "claudecode", Name: "Retained", Configuration: "isolated", Directory: t.TempDir(), Enabled: true}
	if _, err := db.SQL().ExecContext(t.Context(), `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled) VALUES(?,?,?,?,?,?)`, c.ID, c.Kind, c.Name, c.Configuration, c.Directory, c.Enabled); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	s, id := New(db), sessionID()
	if err := s.BindSession(t.Context(), id, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := New(db).SessionConnection(t.Context(), id)
	if err != nil || got != c.ID {
		t.Fatalf("reopen lost routing identity: %q %v", got, err)
	}
}
