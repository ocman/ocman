package sessionsvc

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
)

func TestPendingReferenceNeverResolvesThroughExplicitOrInferredPlatform(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{})
	for _, scope := range []string{"", "opencode", "r-offline:opencode"} {
		adapter, err := svc.resolve(t.Context(), identity.PendingReference(), scope)
		if !errors.Is(err, platforms.ErrPlatformUnreachable) || adapter != nil {
			t.Fatalf("pending reference acquired a platform in %q: %+v %v", scope, adapter, err)
		}
	}
}
