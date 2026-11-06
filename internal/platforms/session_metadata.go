package platforms

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
)

// SessionMetadataReader supplies an indexed row without transcript aggregates
// or live runtime reads. It uses the adapter's session-ID contract.
type SessionMetadataReader interface {
	SessionMetadata(context.Context, string) (*db.Session, error)
}

// ReadSessionMetadata prefers bounded metadata reads. Older peers may not expose
// them; only an unsupported operation permits the owner-routed summary fallback.
func ReadSessionMetadata(ctx context.Context, p Platform, id string) (*db.Session, error) {
	if reader, ok := p.(SessionMetadataReader); ok {
		row, err := reader.SessionMetadata(ctx, id)
		if !errors.Is(err, ErrUnsupported) {
			return row, err
		}
	}
	return ReadSessionSummary(ctx, p, id)
}
