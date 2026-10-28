package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
)

// QuarantineState replaces unresolved native associations with lookup tokens.
// Tokens preserve relationships, uniqueness and feature records, but do not
// allocate a session for a disconnected owner. Durable compatibility rows are
// written before the atomic core rekey, making interrupted migration replayable.
func (s *ReferenceStore) QuarantineState(ctx context.Context, core *state.DB, scope string) error {
	var refs []string
	var err error
	if scope == "" {
		refs, err = core.UnattributedSessionReferences(ctx)
	} else {
		refs, err = core.SessionReferences(ctx, scope)
	}
	if err != nil {
		return err
	}
	mapping := map[string]string{}
	for _, ref := range refs {
		if sessioncatalog.IsID(ref) {
			continue
		}
		if identity.IsPendingReference(ref) {
			row, err := s.get(ctx, ref)
			if err != nil {
				return err
			}
			if row.Scope != scope {
				return sessioncatalog.ErrBindingConflict
			}
			if row.CanonicalID != "" {
				mapping[ref] = row.CanonicalID
			}
			continue
		}
		row, err := s.reserve(ctx, scope, ref)
		if err != nil {
			return err
		}
		mapping[ref] = row.ID
		if row.CanonicalID != "" {
			mapping[ref] = row.CanonicalID
		}
	}
	if scope == "" {
		return core.QuarantineUnattributedSessionReferences(ctx, mapping)
	}
	return core.RekeySessionReferences(ctx, scope, mapping)
}
