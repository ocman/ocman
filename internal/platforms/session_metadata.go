package platforms

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
)

// SessionMetadataReader supplies an indexed row without transcript aggregates
// or live runtime reads. It uses the adapter's session-ID contract.
type SessionMetadataReader interface {
	SessionMetadata(context.Context, string) (*db.Session, error)
}
