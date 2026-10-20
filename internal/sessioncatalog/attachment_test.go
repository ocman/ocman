package sessioncatalog

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestAttachedIdentityRetainsOriginButCannotResolveUntilReady(t *testing.T) {
	s, _ := openStore(t)
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	attached, err := s.Attach(t.Context(), row.ID, binding())
	if err != nil || attached.CreationState != Configuring || attached.NativeID != binding().NativeID {
		t.Fatalf("attach: %+v %v", attached, err)
	}
	if again, err := s.Attach(t.Context(), row.ID, binding()); err != nil || again != attached {
		t.Fatalf("repeated attach: %+v %v", again, err)
	}
	if _, err := s.Resolve(t.Context(), row.OwnerID, row.ID); !errors.Is(err, ErrCreating) {
		t.Fatalf("unfinished identity resolved: %v", err)
	}
	announced, created, err := s.Discover(t.Context(), Discovery{Binding: binding(), Directory: row.Directory})
	if err != nil || created || announced.ID != row.ID || announced.OriginID != row.OriginID {
		t.Fatalf("native announcement split identity: %+v %v %v", announced, created, err)
	}
	b := binding()
	b.NativeID = "replacement"
	if _, err := s.Attach(t.Context(), row.ID, b); !errors.Is(err, ErrTransition) {
		t.Fatalf("attached identity changed: %v", err)
	}
	ready, err := s.Bind(t.Context(), row.ID, binding())
	if err != nil || ready.CreationState != Ready {
		t.Fatalf("completion: %+v %v", ready, err)
	}
	if again, err := s.Attach(t.Context(), row.ID, binding()); err != nil || again != ready {
		t.Fatalf("ready identity downgraded: %+v %v", again, err)
	}
}

func TestAttachedNativeIdentitySurvivesRestartAndConfirmedDisposal(t *testing.T) {
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
	if _, err := s.Attach(t.Context(), row.ID, binding()); err != nil {
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
	replayed, created, err := s.Reserve(t.Context(), reservation())
	if err != nil || created || replayed.ID != row.ID || replayed.NativeID != binding().NativeID || replayed.CreationState != Configuring {
		t.Fatalf("restart lost native binding: %+v %v %v", replayed, created, err)
	}
	failed, err := s.Fail(t.Context(), row.ID, "configuration failed; native session disposed")
	if err != nil || failed.CreationState != CreationFailed || failed.NativeID != binding().NativeID {
		t.Fatalf("disposal lost identity: %+v %v", failed, err)
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); !errors.Is(err, ErrTransition) {
		t.Fatalf("failed native binding reused: %v", err)
	}
}
