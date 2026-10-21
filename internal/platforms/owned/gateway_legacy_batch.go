package owned

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func (g *IdentityGateway) ResolveLegacyReferences(ctx context.Context, scope string, refs []string) (map[string]platforms.SessionIdentity, error) {
	if g == nil || g.store == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	if len(refs) < 1 || len(refs) > 500 {
		return nil, g.invalid("legacy reference batch requires 1..500 references")
	}
	p, err := g.pick(ctx, scope)
	if err != nil {
		return nil, platforms.ErrPlatformUnreachable
	}
	if !CanonicalSessionIDs(p) {
		return nil, platforms.ErrUnsupported
	}
	if owner, base := platforms.SplitPlatformID(string(p.ID())); owner != "" {
		resolver, ok := p.(platforms.LegacySessionResolver)
		if !ok {
			return nil, platforms.ErrUnsupported
		}
		rows, err := resolver.ResolveLegacyReferences(ctx, refs)
		if err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		seen := map[string]bool{}
		for _, ref := range refs {
			wanted[ref] = true
		}
		for ref, row := range rows {
			if !wanted[ref] || row.OwnerID != owner || row.Platform != base || row.NativeID != "" || row.NativeParentID != "" || !sessioncatalog.IsID(row.ID) || seen[row.ID] {
				return nil, sessioncatalog.ErrBindingConflict
			}
			seen[row.ID] = true
		}
		for ref, row := range rows {
			if err := g.store.MirrorCanonical(ctx, identityRecord(&row)); err != nil {
				return nil, err
			}
			row.Platform = string(p.ID())
			rows[ref] = row
		}
		return rows, nil
	}
	out := make(map[string]platforms.SessionIdentity, len(refs))
	for _, ref := range refs {
		info, err := g.LookupReference(ctx, scope, ref)
		if errors.Is(err, platforms.ErrNotFound) || errors.Is(err, sessioncatalog.ErrCreating) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[ref] = *info
	}
	return out, nil
}
