package owned

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// LocalCaller resolves transport correlation only from durable local metadata.
// Native IDs are source-less hints: accept exactly one binding, never adopt a
// native session, read its conversation, or select the newest/default account.
func (g *IdentityGateway) LocalCaller(ctx context.Context, kind, reference string) (*db.Session, error) {
	if g == nil || g.store == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	var row sessioncatalog.Session
	var err error
	if sessioncatalog.IsID(reference) {
		row, err = g.store.GetCanonical(ctx, reference)
	} else {
		row, err = g.store.FindLegacyBinding(ctx, g.owner, kind, reference)
	}
	if err != nil {
		return nil, err
	}
	if row.OwnerID != g.owner || row.Platform != kind || row.Purged {
		return nil, platforms.ErrNotFound
	}
	p, err := g.OwnerPlatform(ctx, row.ID, "")
	if err != nil {
		return nil, err
	}
	reader, ok := p.(interface {
		StoredSessionIdentity(context.Context, string) (*platforms.SessionIdentity, error)
	})
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	if _, err := reader.StoredSessionIdentity(ctx, row.ID); err != nil {
		return nil, err
	}
	return &db.Session{ID: row.ID, Platform: registryPlatformKey(g.registry(), p), Directory: row.Directory, Title: row.Title}, nil
}
