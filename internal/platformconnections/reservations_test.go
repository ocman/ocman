package platformconnections

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func reserve(t *testing.T, s *Store) Reservation {
	t.Helper()
	r, err := s.Reserve(t.Context(), "codex", "isolated", "/accounts/codex")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReservationRestartAndIdempotentSave(t *testing.T) {
	path := statetest.Path(t, Schema)
	db, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db)
	initialize(t, s)
	r := reserve(t, s)
	if r.ID == r.ConnectionID || r.Status != "pending" {
		t.Fatalf("reservation identity: %+v", r)
	}
	if got, err := s.Read(t.Context()); err != nil || len(got.Connections) != 0 {
		t.Fatalf("reservation appeared as connection: %+v %v", got, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	s = New(db)
	if got, err := s.Reservation(t.Context(), r.ID); err != nil || got != r {
		t.Fatalf("reservation changed on restart: %+v %v", got, err)
	}
	c, err := s.SaveReservation(t.Context(), r.ID, " Work ", false)
	if err != nil || c.ID != r.ConnectionID || c.Kind != r.Kind || c.Configuration != r.Configuration || c.Directory != r.Directory || c.Name != "Work" || c.Enabled || c.LegacyKind != "" {
		t.Fatalf("saved connection: %+v %v", c, err)
	}
	if err := s.Rename(t.Context(), c.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	c.Name = "Renamed"
	if got, err := s.SaveReservation(t.Context(), r.ID, "stale name", true); err != nil || got != c {
		t.Fatalf("retry overwrote current state: %+v %v", got, err)
	}
	if err := s.CancelReservation(t.Context(), r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel detached a saved connection: %v", err)
	}
	if got, err := s.Reservation(t.Context(), r.ID); err != nil || got.Status != "saved" {
		t.Fatalf("save receipt: %+v %v", got, err)
	}
	if got, err := s.Read(t.Context()); err != nil || !reflect.DeepEqual(got.Connections, []Connection{c}) || got.DefaultID != "" {
		t.Fatalf("save changed collection/default: %+v %v", got, err)
	}
	if err := s.Remove(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SaveReservation(t.Context(), r.ID, "Work", true); !errors.Is(err, ErrUnavailable) || !got.Removed {
		t.Fatalf("retry resurrected removal: %+v %v", got, err)
	}
}

func TestReservationCancelAndCapacity(t *testing.T) {
	s, _ := openStore(t)
	if _, err := s.Reserve(t.Context(), "codex", "isolated", "/accounts/codex"); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("reserve before initialization: %v", err)
	}
	initialize(t, s)
	for range maxConnections - 1 {
		create(t, s, "Existing")
	}
	r := reserve(t, s)
	if _, err := s.Create(t.Context(), input("extra")); !errors.Is(err, ErrLimit) {
		t.Fatalf("create ignored reserved capacity: %v", err)
	}
	if _, err := s.Reserve(t.Context(), "codex", "isolated", "/another"); !errors.Is(err, ErrLimit) {
		t.Fatalf("reserve exceeded capacity: %v", err)
	}
	for range 2 {
		if err := s.CancelReservation(t.Context(), r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SaveReservation(t.Context(), r.ID, "late", true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("saved canceled reservation: %v", err)
	}
	if got, err := s.Reservation(t.Context(), r.ID); err != nil || got.Status != "canceled" || got.Directory != r.Directory {
		t.Fatalf("cancellation destroyed receipt: %+v %v", got, err)
	}
	last := reserve(t, s)
	if _, err := s.SaveReservation(t.Context(), last.ID, "last", true); err != nil {
		t.Fatalf("saving at reserved capacity failed: %v", err)
	}
	if _, err := s.Create(t.Context(), input("extra")); !errors.Is(err, ErrLimit) {
		t.Fatalf("save freed capacity instead of transferring it: %v", err)
	}
}

func TestReservationConcurrentSaves(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	r := reserve(t, s)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, err := New(db).SaveReservation(t.Context(), r.ID, "Account", true)
			if err != nil || got.ID != r.ConnectionID {
				t.Errorf("concurrent save: %+v %v", got, err)
			}
		})
	}
	wg.Wait()
	got, err := s.Read(t.Context())
	if err != nil || len(got.Connections) != 1 || got.Connections[0].ID != r.ConnectionID {
		t.Fatalf("concurrent save duplicated connection: %+v %v", got, err)
	}
}

func TestReservationConcurrentCapacity(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	for range maxConnections - 1 {
		create(t, s, "Account")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() { _, err := New(db).Reserve(t.Context(), "codex", "existing", "/codex"); errs <- err })
	}
	wg.Wait()
	close(errs)
	success, limited := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 15 {
		t.Fatalf("reserved beyond capacity: %d success %d limited", success, limited)
	}
}

func TestReservationErrors(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	if _, err := s.Reserve(t.Context(), "unknown", "isolated", "/bad"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid reserve: %v", err)
	}
	if _, err := s.SaveReservation(t.Context(), "missing", " ", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid save: %v", err)
	}
	if _, err := s.Reservation(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing reservation: %v", err)
	}
	if _, err := s.SaveReservation(t.Context(), "missing", "Name", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing save: %v", err)
	}
	if err := s.CancelReservation(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing cancel: %v", err)
	}
	r := reserve(t, s)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(t.Context(), "codex", "isolated", "/account"); err == nil {
		t.Fatal("reserved on closed database")
	}
	if _, err := s.SaveReservation(t.Context(), r.ID, "Name", true); err == nil {
		t.Fatal("saved on closed database")
	}
}

func TestReservationSaveFailureRollsBack(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER fail_save BEFORE INSERT ON platform_connection BEGIN SELECT RAISE(ABORT,'failure'); END`,
		`CREATE TRIGGER fail_save BEFORE UPDATE ON platform_connection_setup WHEN NEW.status='saved' BEGIN SELECT RAISE(ABORT,'failure'); END`,
	} {
		s, db := openStore(t)
		initialize(t, s)
		r := reserve(t, s)
		if _, err := db.SQL().ExecContext(t.Context(), trigger); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SaveReservation(t.Context(), r.ID, "Account", true); err == nil {
			t.Fatal("save succeeded through write failure")
		}
		if got, err := s.Reservation(t.Context(), r.ID); err != nil || got != r {
			t.Fatalf("failed save consumed receipt: %+v %v", got, err)
		}
		if _, err := s.Get(t.Context(), r.ConnectionID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("failed save left a connection: %v", err)
		}
		if _, err := db.SQL().ExecContext(t.Context(), `DROP TRIGGER fail_save`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SaveReservation(t.Context(), r.ID, "Account", true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReservationSaveAcrossDatabaseHandles(t *testing.T) {
	path := statetest.Path(t, Schema)
	var stores []*Store
	for range 2 {
		db, err := state.Open(path, Schema)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		stores = append(stores, New(db))
	}
	initialize(t, stores[0])
	r := reserve(t, stores[0])
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			got, err := stores[i%2].SaveReservation(t.Context(), r.ID, "Account", true)
			if err != nil || got.ID != r.ConnectionID {
				t.Errorf("save through separate handles: %+v %v", got, err)
			}
		})
	}
	wg.Wait()
	got, err := stores[0].Read(t.Context())
	if err != nil || len(got.Connections) != 1 {
		t.Fatalf("separate handles duplicated connection: %+v %v", got, err)
	}
}
