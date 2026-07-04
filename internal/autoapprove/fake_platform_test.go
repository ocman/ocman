package autoapprove

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// fakePlatform is a minimal platforms.Platform test double. It embeds
// the interface so any method the tests don't stub panics loudly if
// called; the auto-approve tests only exercise ID and
// RespondPermission.
type fakePlatform struct {
	platforms.Platform

	// id is the platform identifier reported by ID(); defaults to
	// "fake" when unset (matching the server package's shared fake).
	id platforms.ID

	// respondPermissionFn, when non-nil, intercepts RespondPermission
	// calls so tests can observe replies without a real adapter.
	respondPermissionFn func(req platforms.RespondPermissionRequest) error
}

func (f *fakePlatform) ID() platforms.ID {
	if f.id == "" {
		return "fake"
	}
	return f.id
}

func (f *fakePlatform) RespondPermission(_ context.Context, req platforms.RespondPermissionRequest) error {
	if f.respondPermissionFn != nil {
		return f.respondPermissionFn(req)
	}
	return nil
}
