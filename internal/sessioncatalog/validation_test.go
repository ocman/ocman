package sessioncatalog

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestReservationsRejectIncompleteOriginsAndMissingParents(t *testing.T) {
	s, _ := openStore(t)
	for _, change := range []func(*Reservation){
		func(r *Reservation) { r.RequestKey = " " },
		func(r *Reservation) { r.PayloadHash = "" },
		func(r *Reservation) { r.PayloadHash = "invalid" },
		func(r *Reservation) { r.OwnerID = "" },
		func(r *Reservation) { r.Platform = "" },
		func(r *Reservation) { r.Directory = " " },
		func(r *Reservation) { r.OriginID = "" },
		func(r *Reservation) { r.Origin = User },
		func(r *Reservation) { r.Origin = External; r.OriginID = "" },
		func(r *Reservation) { r.Origin = "unknown" },
	} {
		r := reservation()
		change(&r)
		if _, _, err := s.Reserve(t.Context(), r); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %+v: %v", r, err)
		}
	}
	r := reservation()
	r.ParentID = "missing"
	if _, _, err := s.Reserve(t.Context(), r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
	r.ParentID = ""
	r.Origin = User
	r.OriginID = ""
	parent, _, err := s.Reserve(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.RequestKey = "child"
	r.ParentID = parent.ID
	r.Origin = Factory
	r.OriginID = "attempt-1"
	child, _, err := s.Reserve(t.Context(), r)
	if err != nil || child.ParentID != parent.ID {
		t.Fatalf("child: %+v %v", child, err)
	}
	r.OwnerID = "other-owner"
	if _, _, err := s.Reserve(t.Context(), r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-owner parent: %v", err)
	}
}

func TestReadyIdentityCannotBeFailed(t *testing.T) {
	s, _ := openStore(t)
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fail(t.Context(), row.ID, "late failure"); !errors.Is(err, ErrTransition) {
		t.Fatalf("ready downgraded: %v", err)
	}
	r := reservation()
	r.RequestKey = "failed"
	failed, _, err := s.Reserve(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fail(t.Context(), failed.ID, "no launch"); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogueSurvivesRestartWithoutReassigningIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s, err = New(db)
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := s.Reserve(t.Context(), reservation())
	if err != nil || created || again.ID != row.ID || again.CreationState != Ready {
		t.Fatalf("restart reassigned: %+v %v %v", again, created, err)
	}
}

func TestInvalidQueriesAndUnavailableStore(t *testing.T) {
	s, db := openStore(t)
	if _, err := New(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.FindBinding(t.Context(), Binding{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), "missing", Binding{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Fail(t.Context(), "missing", " "); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, args := range []struct {
		owner         string
		limit, offset int
	}{{"", 10, 0}, {"owner", 0, 0}, {"owner", 501, 0}, {"owner", 10, -1}} {
		if _, err := s.List(t.Context(), args.owner, args.limit, args.offset); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Reserve(t.Context(), reservation()); err == nil {
		t.Fatal("reserve on closed database")
	}
	if _, err := s.Bind(t.Context(), "missing", binding()); err == nil {
		t.Fatal("bind on closed database")
	}
	if _, err := s.Fail(t.Context(), "missing", "failure"); err == nil {
		t.Fatal("failure on closed database")
	}
	if _, err := s.List(t.Context(), "owner", 10, 0); err == nil {
		t.Fatal("list on closed database")
	}
}
