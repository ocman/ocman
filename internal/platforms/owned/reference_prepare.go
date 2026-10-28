package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// PrepareReference supplies feature-owned pre-serving migrations with the same
// proof/quarantine rules as core state. Native provenance stays in this store;
// feature tables receive only canonical IDs or non-executable lookup tokens.
func (s *ReferenceStore) PrepareReference(ctx context.Context, catalogue *sessioncatalog.Store, owner, scope, reference string) (string, error) {
	if catalogue == nil || owner == "" || reference == "" {
		return "", sessioncatalog.ErrInvalid
	}
	if sessioncatalog.IsID(reference) {
		return reference, nil
	}
	var ref legacyReference
	var err error
	if identity.IsPendingReference(reference) {
		ref, err = s.get(ctx, reference)
		if err == nil && ref.Scope != scope {
			return "", sessioncatalog.ErrBindingConflict
		}
	} else {
		ref, err = s.reserve(ctx, scope, reference)
	}
	if err != nil {
		return "", err
	}
	if ref.CanonicalID != "" {
		return ref.CanonicalID, nil
	}
	if remote, _ := platforms.SplitPlatformID(scope); scope == "" || remote != "" {
		return ref.ID, nil
	}
	row, err := catalogue.FindLegacyBinding(ctx, owner, scope, ref.NativeID)
	if errors.Is(err, sessioncatalog.ErrNotFound) || errors.Is(err, sessioncatalog.ErrBindingConflict) {
		return ref.ID, nil
	}
	if err != nil {
		return "", err
	}
	if row.CreationState != sessioncatalog.Ready {
		return ref.ID, nil
	}
	if err := catalogue.RetainLegacyKeys(ctx, owner, row.ID); err != nil {
		return "", err
	}
	if err := s.bind(ctx, scope, ref.ID, row.ID); err != nil {
		return "", err
	}
	return row.ID, nil
}
