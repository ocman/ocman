package platforms

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// RuntimeSessionReader provides uncached owner-local replacement candidates and
// persisted abort evidence. Returned session references follow the adapter's
// identity contract; native message identifiers remain unchanged.
type RuntimeSessionReader interface {
	GetSessionIdentities(context.Context) ([]db.Session, error)
	GetSessionLifecycle(context.Context, string) (db.SessionLifecycle, error)
}

// WorktreeRuntimeReader supplies strict native safety probes for destructive
// checkout maintenance. Unlike migration inventory, an unavailable history root
// or supervisor is an error, never an authoritative empty source.
type WorktreeRuntimeReader interface {
	WorktreeRuntimeSessions(context.Context) ([]db.Session, error)
	WorktreeRuntimeLifecycle(context.Context, string) (db.SessionLifecycle, error)
}
