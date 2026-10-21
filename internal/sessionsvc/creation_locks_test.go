package sessionsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestCreationLockScopesOwnersAndRequestsAndReleasesCancelledWaiters(t *testing.T) {
	s := &Service{}
	acquire := func(platform platforms.ID, key string) func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		unlock, err := s.lockCreation(ctx, platform, key)
		if err != nil {
			t.Fatal(err)
		}
		return unlock
	}
	local := acquire("opencode", "same")
	defer local()
	remote := acquire("r-box:opencode", "same")
	remote()
	otherRequest := acquire("claudecode", "different")
	otherRequest()
	// Catalogue request keys are owner-wide, including across local platforms.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if unlock, err := s.lockCreation(ctx, "claudecode", "same"); unlock != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter bypassed owner-wide serialization: %v", err)
	}
	local()
	local() // callers cannot release another operation through a double cleanup
	unlock := acquire("opencode", "same")
	unlock()
	ctx, stop := context.WithCancel(t.Context())
	stop()
	if unlock, err := s.lockCreation(ctx, "opencode", "cancelled"); unlock != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request acquired creation ownership: %v", err)
	}
	if len(s.creations.entries) != 0 {
		t.Fatalf("completed request keys leaked %d creation locks", len(s.creations.entries))
	}
}
