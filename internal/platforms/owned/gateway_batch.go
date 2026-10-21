package owned

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func (g *IdentityGateway) LookupBatch(ctx context.Context, platformID string, nativeIDs []string) ([]platforms.SessionIdentity, error) {
	if g == nil || g.store == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	if len(nativeIDs) < 1 || len(nativeIDs) > 500 {
		return nil, g.invalid("identity batch requires 1..500 native IDs")
	}
	if platformID == "" {
		return g.lookupNative(ctx, "", nativeIDs)
	}
	p, err := g.pick(ctx, platformID)
	if err != nil {
		return nil, platforms.ErrPlatformUnreachable
	}
	if CanonicalSessionIDs(p) {
		return g.lookupNative(ctx, platformID, nativeIDs)
	}
	if owner, base := platforms.SplitPlatformID(string(p.ID())); owner != "" {
		resolver, ok := p.(platforms.SessionIdentityResolver)
		if !ok {
			return nil, platforms.ErrUnsupported
		}
		rows, err := resolver.SessionIdentities(ctx, nativeIDs)
		if err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		for _, id := range nativeIDs {
			wanted[id] = true
		}
		for i := range rows {
			row := &rows[i]
			if row.OwnerID != owner || row.OwnerID != resolver.SessionOwnerID() || row.Platform != base || !wanted[row.NativeID] {
				return nil, sessioncatalog.ErrBindingConflict
			}
			delete(wanted, row.NativeID)
			if err := g.store.Mirror(ctx, identityRecord(row)); err != nil {
				return nil, err
			}
			row.Platform = string(p.ID())
		}
		return rows, nil
	}
	return g.lookupNative(ctx, string(p.ID()), nativeIDs)
}

func (g *IdentityGateway) lookupNative(ctx context.Context, platformID string, ids []string) ([]platforms.SessionIdentity, error) {
	seen := map[string]bool{}
	rows := make([]platforms.SessionIdentity, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		info, err := g.Lookup(ctx, platformID, platforms.SessionIdentityRequest{NativeID: id})
		if errors.Is(err, sessioncatalog.ErrCreating) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, *info)
	}
	return rows, nil
}
