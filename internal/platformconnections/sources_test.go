package platformconnections

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func TestReadSourceBindingRequiresExactPersistedAccountAndKind(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	for _, tc := range []struct {
		id, kind, source string
		want             error
	}{
		{c.ID, c.Kind, "", ErrInvalid}, {"cnx_missing", c.Kind, "source", ErrNotFound}, {c.ID, "codex", "source", ErrInvalid},
	} {
		if err := s.BindSource(t.Context(), tc.id, tc.kind, tc.source); !errors.Is(err, tc.want) {
			t.Fatalf("invalid provenance accepted: %v", err)
		}
	}
	if _, err := s.LegacySourceImportAllowed(t.Context(), "cnx_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.BindSource(t.Context(), c.ID, c.Kind, "source"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveReadSource(t.Context(), c.Kind, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.ResolveReadSource(t.Context(), c.Kind, "source"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := r.BindRead(t.Context(), "cnx_missing", &platformtest.Fake{PlatformID: c.Kind}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := r.BindRead(t.Context(), c.ID, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := r.BindRead(t.Context(), c.ID, &platformtest.Fake{PlatformID: "codex"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSource(t.Context(), c.ID, c.Kind, "new-source"); !errors.Is(err, ErrInvalid) {
		t.Fatal("removed account acquired new provenance", err)
	}
	p := &platformtest.Fake{PlatformID: c.Kind}
	if err := r.BindRead(t.Context(), c.ID, p); err != nil {
		t.Fatal(err)
	}
	if err := r.BindRead(t.Context(), c.ID, p); !errors.Is(err, ErrAlreadyBound) {
		t.Fatal(err)
	}
	if got, err := r.ResolveReadSource(t.Context(), c.Kind, "source"); err != nil || got != p {
		t.Fatal("tombstone lost proven history", err)
	}
	if _, err := r.ResolveSource(t.Context(), c.Kind, "source"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("retained binding granted execution", err)
	}
}

func TestSourceProvenanceStoreOutageFailsClosed(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SourceKeys(t.Context(), c.ID); err == nil {
		t.Fatal("source keys hid outage")
	}
	if _, err := s.RetainedConnections(t.Context()); err == nil {
		t.Fatal("retained collection hid outage")
	}
	if _, err := s.LegacySourceImportAllowed(t.Context(), c.ID); err == nil {
		t.Fatal("legacy import hid outage")
	}
	if err := s.BindSource(t.Context(), c.ID, c.Kind, "source"); err == nil {
		t.Fatal("binding hid outage")
	}
}
