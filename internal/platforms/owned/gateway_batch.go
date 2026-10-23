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
	return g.lookupNative(ctx, platformID, nativeIDs)
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
