package pluginsmod

import (
	"context"
	"errors"
	"testing"

	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestSessionProjectReservationFencesConcurrentCreation(t *testing.T) {
	store := openPluginStore(t)
	type result struct {
		claim *sessionProjectReservation
		err   error
	}
	results := make(chan result, 8)
	for i := range 8 {
		go func() {
			claim, err := store.reserveSessionProject(t.Context(), "reader", string(rune('a'+i)), "/private/source", "/projects/platform-service")
			results <- result{claim, err}
		}()
	}
	var winner *sessionProjectReservation
	for range 8 {
		r := <-results
		if r.err == nil {
			if winner != nil || r.claim == nil {
				t.Fatal("concurrent creation received more than one claim")
			}
			winner = r.claim
		} else if !errors.Is(r.err, plugins.ErrUnavailable) {
			t.Fatalf("unexpected pending claim error: %v", r.err)
		}
	}
	if winner == nil {
		t.Fatal("no creation admitted")
	}
	if associations, err := store.sessionProjectAssociations(t.Context()); err != nil || len(associations) != 0 {
		t.Fatalf("provisional claim became visible: %+v %v", associations, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := winner.commit(canceled); err != nil {
		t.Fatalf("created session lost attribution after cancellation: %v", err)
	}
	if err := winner.release(t.Context()); err != nil {
		t.Fatal(err)
	}
	existing, err := store.reserveSessionProject(t.Context(), "reader", "existing", "/private/source", "/projects/platform-service")
	if err != nil || existing != nil {
		t.Fatalf("committed association was claimed again: %+v %v", existing, err)
	}
	if err := existing.release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if associations, err := store.sessionProjectAssociations(t.Context()); err != nil || associations["/private/source"] != "/projects/platform-service" {
		t.Fatalf("cleanup removed committed attribution: %+v %v", associations, err)
	}
}

func TestSessionProjectReservationCleanupCannotDeleteAnotherOperation(t *testing.T) {
	store := openPluginStore(t)
	first, err := store.reserveSessionProject(t.Context(), "reader", "first", "/private/source", "/projects/wrong")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := first.release(canceled); err != nil {
		t.Fatalf("canceled creation retained its claim: %v", err)
	}
	second, err := store.reserveSessionProject(t.Context(), "reader", "second", "/private/source", "/projects/correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := first.commit(t.Context()); err == nil {
		t.Fatal("stale operation committed another operation's claim")
	}
	if err := second.commit(t.Context()); err != nil {
		t.Fatalf("stale cleanup deleted the winning claim: %v", err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.commit(t.Context()); err == nil {
		t.Fatal("commit storage error hidden")
	}
	if err := second.release(t.Context()); err == nil {
		t.Fatal("release storage error hidden")
	}
}
