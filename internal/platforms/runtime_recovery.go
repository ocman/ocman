package platforms

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
)

// RuntimeSessionReader provides uncached owner-local replacement candidates and
// persisted abort evidence. Returned session references follow the adapter's
// identity contract; native message identifiers remain unchanged.
type RuntimeSessionReader interface {
	GetSessionIdentities(context.Context) ([]db.Session, error)
	GetSessionLifecycle(context.Context, string) (db.SessionLifecycle, error)
}
