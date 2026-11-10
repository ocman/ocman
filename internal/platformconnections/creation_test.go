package platformconnections

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func creationRouter(t *testing.T) (*Router, Connection, Connection) {
	t.Helper()
	s, _ := openStore(t)
	initialize(t, s)
	a, b := create(t, s, "A"), create(t, s, "B")
	r := NewRouter(s)
	for _, c := range []Connection{a, b} {
		if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDefault(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	return r, a, b
}

func TestCreationReceiptPinsAccountAndGeneratedIdentity(t *testing.T) {
	r, a, b := creationRouter(t)
	first, err := r.AdmitCreation(t.Context(), "request", "", Reference{})
	if err != nil || first.Connection.ID != a.ID || !identity.IsSessionID(first.SessionID) {
		t.Fatalf("initial receipt: %+v %v", first, err)
	}
	if err := r.store.SetDefault(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the in-memory registry; both the identity and the account
	// must come from the database, not the old router or current default.
	restarted := NewRouter(r.store)
	if err := restarted.Bind(t.Context(), a.ID, first.Platform); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []Reference{{}, {ConnectionID: a.ID}} {
		retry, err := restarted.AdmitCreation(t.Context(), "request", "", ref)
		if err != nil || retry.SessionID != first.SessionID || retry.Connection.ID != a.ID {
			t.Fatalf("retry changed identity or account: %+v %v", retry, err)
		}
	}
	if got, err := restarted.ResolveSession(t.Context(), first.SessionID); err != nil || got.Connection.ID != a.ID {
		t.Fatalf("creation receipt did not pin session routing: %+v %v", got, err)
	}
	if _, err := r.AdmitCreation(t.Context(), "request", "", Reference{ConnectionID: b.ID}); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("explicit retry selected another account: %v", err)
	}
	if err := r.store.Remove(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AdmitCreation(t.Context(), "request", "", Reference{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("removed account fell through to current default: %v", err)
	}
}

func TestCreationReceiptRejectsChangedSessionAndRequest(t *testing.T) {
	r, a, _ := creationRouter(t)
	id := sessionID()
	first, err := r.AdmitCreation(t.Context(), "request", id, Reference{ConnectionID: a.ID})
	if err != nil || first.SessionID != id {
		t.Fatalf("explicit identity changed: %+v %v", first, err)
	}
	if _, err := r.AdmitCreation(t.Context(), "request", sessionID(), Reference{}); !errors.Is(err, ErrCreationConflict) {
		t.Fatalf("request changed its identity: %v", err)
	}
	if _, err := r.AdmitCreation(t.Context(), "another-request", id, Reference{}); !errors.Is(err, ErrCreationConflict) {
		t.Fatalf("identity acquired a second request: %v", err)
	}
}

func TestConcurrentCreationReceiptsAllocateOneIdentity(t *testing.T) {
	r, a, b := creationRouter(t)
	ids := make(chan string, 12)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			c := a
			if i%2 != 0 {
				c = b
			}
			route, err := r.AdmitCreation(t.Context(), "request", "", Reference{ConnectionID: c.ID})
			if errors.Is(err, ErrSessionConflict) {
				return
			}
			if err != nil {
				t.Errorf("concurrent admission: %v", err)
				return
			}
			ids <- route.SessionID
		})
	}
	wg.Wait()
	close(ids)
	var winner string
	for id := range ids {
		if winner == "" {
			winner = id
		}
		if id != winner {
			t.Fatal("a single retry key allocated multiple identities")
		}
	}
	if winner == "" {
		t.Fatal("no successful admission")
	}
}

func TestCreationAdmissionValidationAndUnavailableStore(t *testing.T) {
	r, a, _ := creationRouter(t)
	for _, key := range []string{"", "  ", "control\nkey", strings.Repeat("x", 513)} {
		if _, err := r.AdmitCreation(t.Context(), key, "", Reference{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid key admitted: %v", err)
		}
	}
	if _, err := r.AdmitCreation(t.Context(), "request", "native-id", Reference{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := NewRouter(nil).AdmitCreation(t.Context(), "request", "", Reference{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.AdmitCreation(t.Context(), "request", "", Reference{ConnectionID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.AdmitCreation(ctx, "request", "", Reference{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := r.store.reserveCreation(ctx, "request", "", a.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := r.store.reserveCreation(t.Context(), "request", "", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := r.store.SetEnabled(t.Context(), a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.reserveCreation(t.Context(), "request", "", a.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
