package sessioncatalog

import (
	"context"

	"github.com/ocman/ocman/internal/statev2"
)

// A partial core migration can leave canonical keys in retained history before
// startup finishes. Legacy indexing validates them, never re-adopts them.
func (m *Module) validateCanonicalMetadata(ctx context.Context, source Source, metadata statev2.SessionMetadata) error {
	row, err := m.deps.Store.Get(ctx, metadata.Session.SessionID)
	if err != nil {
		return err
	}
	if row.OwnerID != source.OwnerID || row.Platform != source.Platform || row.SourceKey != source.SourceKey {
		return ErrBindingConflict
	}
	return nil
}
