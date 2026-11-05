package platforms

import (
	"context"
	"github.com/ocman/ocman/internal/db"
)

// WorktreeSessionReader is the complete persisted maintenance inventory,
// including settled native children hidden from the normal display list.
type WorktreeSessionReader interface {
	WorktreeSessions(context.Context) ([]db.Session, error)
}
