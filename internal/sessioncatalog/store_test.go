package sessioncatalog

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func openStore(t testing.TB) (*Store, *state.DB) {
	t.Helper()
	db := statetest.Open(t, Schema)
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func reservation() Reservation {
	return Reservation{RequestKey: "start-1", PayloadHash: strings.Repeat("ab", 32), OwnerID: "machine-1", Platform: "opencode", Directory: "/project", Title: "A session", Origin: Routine, OriginID: "routine-1"}
}

func binding() Binding {
	return Binding{OwnerID: "machine-1", Platform: "opencode", SourceKey: "source-1", NativeID: "native-1"}
}

func TestReservePersistsOriginBeforeBindingAndReplays(t *testing.T) {
	s, _ := openStore(t)
	req := reservation()
	row, created, err := s.Reserve(t.Context(), req)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", row, created, err)
	}
	if len(row.ID) != 40 || row.ID[:4] != "ocm_" || row.CreationState != Creating || row.Origin != Routine || row.OriginID != req.OriginID || row.NativeID != "" {
		t.Fatalf("incomplete reservation: %+v", row)
	}
	rows, err := s.List(t.Context(), req.OwnerID, 10, 0)
	if err != nil || len(rows) != 1 || rows[0] != row {
		t.Fatalf("list reservation: %+v, %v", rows, err)
	}
	replayed, created, err := s.Reserve(t.Context(), req)
	if err != nil || created || replayed != row {
		t.Fatalf("replay: %+v %v %v", replayed, created, err)
	}
	req.Title = "changed"
	if _, _, err := s.Reserve(t.Context(), req); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("changed request: %v", err)
	}
	ready, err := s.Bind(t.Context(), row.ID, binding())
	if err != nil || ready.ID != row.ID || ready.CreationState != Ready || ready.OriginID != row.OriginID {
		t.Fatalf("bind: %+v, %v", ready, err)
	}
	again, err := s.Bind(t.Context(), row.ID, binding())
	if err != nil || again != ready {
		t.Fatalf("replayed bind: %+v %v", again, err)
	}
	byNative, err := s.FindBinding(t.Context(), binding())
	if err != nil || byNative.ID != row.ID {
		t.Fatalf("native lookup: %+v %v", byNative, err)
	}
	req.Title = row.Title
	unchangedHash := req.PayloadHash
	req.PayloadHash = strings.Repeat("cd", 32)
	if _, _, err := s.Reserve(t.Context(), req); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("changed execution payload: %v", err)
	}
	req.PayloadHash = unchangedHash
	replayed, created, err = s.Reserve(t.Context(), req)
	if err != nil || created || replayed != ready {
		t.Fatalf("ready replay: %+v %v %v", replayed, created, err)
	}
}

func TestConcurrentReservationHasOneCreator(t *testing.T) {
	s, _ := openStore(t)
	var wg sync.WaitGroup
	type result struct {
		row     Session
		created bool
		err     error
	}
	results := make(chan result, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, created, err := s.Reserve(t.Context(), reservation())
			results <- result{row, created, err}
		}()
	}
	wg.Wait()
	close(results)
	var id string
	creators := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if id == "" {
			id = r.row.ID
		}
		if r.row.ID != id {
			t.Fatalf("two identities: %s, %s", id, r.row.ID)
		}
		if r.created {
			creators++
		}
	}
	if creators != 1 {
		t.Fatalf("%d creators", creators)
	}
}

func TestBindingCannotMoveOrStealIdentity(t *testing.T) {
	s, _ := openStore(t)
	r := reservation()
	r.RequestKey = "first"
	first, _, err := s.Reserve(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), first.ID, binding()); err != nil {
		t.Fatal(err)
	}
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("stolen binding: %v", err)
	}
	b := binding()
	b.NativeID = "different"
	if _, err := s.Bind(t.Context(), first.ID, b); !errors.Is(err, ErrTransition) {
		t.Fatalf("rebound: %v", err)
	}
	b.OwnerID = "wrong-owner"
	if _, err := s.Bind(t.Context(), row.ID, b); !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner changed: %v", err)
	}
}

func TestFailedCreationIsDurableAndDoesNotAuthorizeRetry(t *testing.T) {
	s, _ := openStore(t)
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.Fail(t.Context(), row.ID, "rejected before launch")
	if err != nil || failed.CreationState != CreationFailed {
		t.Fatalf("fail: %+v %v", failed, err)
	}
	if again, err := s.Fail(t.Context(), row.ID, failed.CreationError); err != nil || again != failed {
		t.Fatalf("failure replay: %+v %v", again, err)
	}
	replayed, created, err := s.Reserve(t.Context(), reservation())
	if err != nil || created || replayed != failed {
		t.Fatalf("failure retried: %+v %v %v", replayed, created, err)
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); !errors.Is(err, ErrTransition) {
		t.Fatalf("failed reservation bound: %v", err)
	}
	if _, err := s.Fail(t.Context(), row.ID, "different"); !errors.Is(err, ErrTransition) {
		t.Fatalf("failure changed: %v", err)
	}
}
