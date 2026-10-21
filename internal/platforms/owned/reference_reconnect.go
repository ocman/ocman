package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

// ResolvePending records authenticated owner proofs during normal operation.
// It never rekeys core/feature tables or makes lookup tokens executable. The
// caller requests a drained reload when any bindings were added; pre-serving
// migrations apply them after all state users have stopped.
func (s *ReferenceStore) ResolvePending(ctx context.Context, gateway *IdentityGateway, scope string) (int, error) {
	owner, _ := platforms.SplitPlatformID(scope)
	if gateway == nil || owner == "" {
		return 0, sessioncatalog.ErrInvalid
	}
	var cursor string
	bound := 0
	for {
		page, err := s.referencePageFiltered(ctx, scope, cursor, true)
		if err != nil {
			return bound, err
		}
		if len(page) == 0 {
			return bound, nil
		}
		var refs []string
		for _, row := range page {
			if row.CanonicalID == "" {
				refs = append(refs, row.NativeID)
			}
		}
		if len(refs) > 0 {
			resolved, err := gateway.ResolveLegacyReferences(ctx, scope, refs)
			if err != nil {
				return bound, err
			}
			for _, row := range page {
				if row.CanonicalID != "" {
					continue
				}
				info, ok := resolved[row.NativeID]
				if !ok {
					continue
				}
				if err := s.bind(ctx, scope, row.ID, info.ID); err != nil {
					return bound, err
				}
				bound++
			}
		}
		cursor = page[len(page)-1].ID
	}
}
