package sessionsvc

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

// beginConnection joins the account's admission fence for the native mutation.
// Prompt answers and aborts intentionally do not pass through this gate, so an
// already-running turn can settle while removal waits for the account to drain.
func (s *Service) beginConnection(ctx context.Context, adapter platforms.Platform, sessionID string) (finish func(), err error) {
	defer func() { err = connectionDispatchError(err) }()
	if s.connections == nil || sessionID == "" {
		return func() {}, nil
	}
	if remote, _ := platforms.SplitPlatformID(string(adapter.ID())); remote != "" {
		// The adapter was selected through the authenticated registry/gateway.
		// A new remote draft need not already be mirrored into the local DB.
		return func() {}, nil
	}
	route, err := s.connections.router.ResolveSession(ctx, sessionID)
	if errors.Is(err, platformconnections.ErrNotFound) {
		row, readErr := s.connections.catalogue.GetCanonical(ctx, sessionID)
		if readErr == nil && row.OwnerID != s.connections.owner {
			return func() {}, nil // Remote owner's service fences its mutation.
		}
	}
	if err != nil {
		return nil, err
	}
	release, err := s.connections.router.Begin(ctx, route.Connection.ID)
	if err != nil {
		return nil, err
	}
	// Persisted intent may have changed between resolution and joining the
	// fence. Never carry an earlier readiness snapshot into native execution.
	if _, err := s.connections.router.ResolveSession(ctx, sessionID); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
