package owned

import (
	"context"

	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
)

// RestoreState resolves quarantined references through the authenticated owner
// gateway. A disconnected owner leaves records untouched. Persist bindings first
// so retries after a crash never ask an owner to assign another identity.
// Like RekeySessionReferences, this requires quiescent session-state users.
// Startup may call it directly; reconnect recovery must pause admission and
// invalidate affected caches before resuming workers.
func (s *ReferenceStore) RestoreState(ctx context.Context, core *state.DB, gateway *IdentityGateway, scope string) error {
	if core == nil || gateway == nil || scope == "" {
		return sessioncatalog.ErrInvalid
	}
	var cursor string
	for {
		page, err := s.referencePage(ctx, scope, cursor)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		var unresolved []string
		for _, row := range page {
			if row.CanonicalID == "" {
				unresolved = append(unresolved, row.NativeID)
			}
		}
		mapping := map[string]string{}
		if len(unresolved) > 0 {
			resolved, err := gateway.ResolveLegacyReferences(ctx, scope, unresolved)
			if err != nil {
				return err
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
					return err
				}
				mapping[row.ID] = info.ID
			}
		}
		for _, row := range page {
			if row.CanonicalID != "" {
				mapping[row.ID] = row.CanonicalID
			}
		}
		if len(mapping) > 0 {
			if err := core.RekeySessionReferences(ctx, scope, mapping); err != nil {
				return err
			}
		}
		cursor = page[len(page)-1].ID
	}
}

func (s *ReferenceStore) referencePage(ctx context.Context, scope, cursor string) ([]legacyReference, error) {
	return s.referencePageFiltered(ctx, scope, cursor, false)
}

func (s *ReferenceStore) referencePageFiltered(ctx context.Context, scope, cursor string, pendingOnly bool) ([]legacyReference, error) {
	query := `SELECT ref_id,scope,native_id,canonical_id FROM platform_session_reference WHERE scope=? AND ref_id>?`
	if pendingOnly {
		query += ` AND canonical_id=''`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY ref_id LIMIT 500`, scope, cursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var page []legacyReference
	for rows.Next() {
		var row legacyReference
		if err := rows.Scan(&row.ID, &row.Scope, &row.NativeID, &row.CanonicalID); err != nil {
			return nil, err
		}
		page = append(page, row)
	}
	return page, rows.Err()
}
