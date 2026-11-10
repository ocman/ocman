package platformconnections

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func TestDetachDrainsAndRestoresOnFailure(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind}); err != nil {
		t.Fatal(err)
	}
	release, err := r.Begin(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Detach(ctx, c.ID, func() error { t.Fatal("removed before drain"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
	release() // release is idempotent.
	want := errors.New("persistence failed")
	if err := r.Detach(t.Context(), c.ID, func() error {
		if _, err := r.Begin(t.Context(), c.ID); !errors.Is(err, ErrUnavailable) {
			t.Errorf("admitted during detach: %v", err)
		}
		return want
	}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	release, err = r.Begin(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := r.Detach(t.Context(), c.ID, func() error { return s.Remove(t.Context(), c.ID) }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Begin(t.Context(), c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestSourceProvenanceNeverMovesToSibling(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	a, b := create(t, s, "A"), create(t, s, "B")
	if err := s.BindSource(t.Context(), a.ID, a.Kind, "source"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSource(t.Context(), a.ID, a.Kind, "source"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSource(t.Context(), b.ID, b.Kind, "source"); !errors.Is(err, ErrAlreadyBound) {
		t.Fatal(err)
	}
	r := NewRouter(s)
	p := &platformtest.Fake{PlatformID: a.Kind}
	if err := r.Bind(t.Context(), a.ID, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSource(t.Context(), a.Kind, "source"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if got, err := r.ResolveReadSource(t.Context(), a.Kind, "source"); err != nil || got != p {
		t.Fatalf("lost retained read: %v", err)
	}
	if _, err := r.ResolveSource(t.Context(), a.Kind, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCompletedDetachAllowsLaterPersistenceWithoutReopeningAdmission(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind}); err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(t.Context(), c.ID, func() error {
		if err := r.Detach(t.Context(), c.ID, func() error { t.Error("overlapping persistence callback"); return nil }); !errors.Is(err, ErrUnavailable) {
			t.Error(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := errors.New("later persistence failed")
	if err := r.Detach(t.Context(), c.ID, func() error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if _, err := r.Begin(t.Context(), c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed later callback cleared completed fence", err)
	}
	if err := r.Detach(t.Context(), c.ID, func() error { return s.Remove(t.Context(), c.ID) }); err != nil {
		t.Fatal(err)
	}
}
