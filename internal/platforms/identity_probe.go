package platforms

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// SessionIdentityProbe proves that a native session belongs to the
// integration's physical source before it is bound to an Ocman identity. It
// returns only binding metadata (title, directory, native parent, creation
// time) and ErrNotFound for a session the source does not have. It is
// identity discovery inside the platform module, never a browsing read.
type SessionIdentityProbe interface {
	SessionIdentityMetadata(context.Context, string) (*db.Session, error)
}
