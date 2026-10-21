package owned

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func (g *IdentityGateway) LookupReference(ctx context.Context, scope, reference string) (*platforms.SessionIdentity, error) {
	if identity.IsPendingReference(reference) {
		return nil, platforms.ErrPlatformUnreachable
	}
	request := platforms.SessionIdentityRequest{ID: reference}
	if !sessioncatalog.IsID(reference) {
		request = platforms.SessionIdentityRequest{NativeID: reference}
	}
	return g.Lookup(ctx, scope, request)
}

// ReferenceMapping is compatibility-only for old wire data. Already canonical
// references require no owner RPC and pending references remain non-executable.
func (g *IdentityGateway) ReferenceMapping(ctx context.Context, scope string, refs []string) (map[string]string, error) {
	out := make(map[string]string, len(refs))
	var legacy []string
	for _, ref := range refs {
		if sessioncatalog.IsID(ref) {
			out[ref] = ref
		} else if !identity.IsPendingReference(ref) {
			legacy = append(legacy, ref)
		}
	}
	if len(legacy) == 0 {
		return out, nil
	}
	canonical := false
	if scope == "" {
		for _, p := range g.registry().Platforms() {
			if remote, _ := platforms.SplitPlatformID(string(p.ID())); remote == "" && CanonicalSessionIDs(p) {
				canonical = true
				break
			}
		}
	} else if p, ok := g.registry().Get(platforms.ID(scope)); ok {
		canonical = CanonicalSessionIDs(p)
	}
	if canonical {
		for _, ref := range legacy {
			info, err := g.LookupReference(ctx, scope, ref)
			if errors.Is(err, sessioncatalog.ErrCreating) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out[ref] = info.ID
		}
		return out, nil
	}
	rows, err := g.LookupBatch(ctx, scope, legacy)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.NativeID] = row.ID
	}
	return out, nil
}
