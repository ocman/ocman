package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// OwnerPlatform resolves authority from durable owner metadata, never the
// registry's best-effort reverse cache. Live execution/source checks stay inside
// the selected adapter. An explicit scope cannot override the persisted owner.
func (g *IdentityGateway) OwnerPlatform(ctx context.Context, id, scope string) (platforms.Platform, error) {
	if identity.IsPendingReference(id) {
		return nil, platforms.ErrPlatformUnreachable
	}
	if g == nil || g.store == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	if !sessioncatalog.IsID(id) {
		return nil, g.invalid("canonical session ID required")
	}
	row, err := g.store.GetCanonical(ctx, id)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		if _, err = g.Lookup(ctx, scope, platforms.SessionIdentityRequest{ID: id}); err == nil {
			row, err = g.store.GetCanonical(ctx, id)
		}
	}
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		return nil, platforms.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.Purged {
		return nil, platforms.ErrNotFound
	}
	if row.CreationState != sessioncatalog.Ready {
		return nil, sessioncatalog.ErrCreating
	}
	expected := row.Platform
	if row.OwnerID != g.owner {
		expected = platforms.CompoundPlatformID(row.OwnerID, row.Platform)
	}
	if scope != "" && scope != expected {
		return nil, platforms.ErrNotFound
	}
	p, ok := g.registry().Get(platforms.ID(expected))
	if !ok {
		return nil, platforms.ErrPlatformUnreachable
	}
	if !CanonicalSessionIDs(p) {
		return nil, platforms.ErrPlatformUnreachable
	}
	return p, nil
}
