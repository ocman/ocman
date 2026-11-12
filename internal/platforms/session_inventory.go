package platforms

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// SessionInventoryReader supplies a complete session inventory for safety-sensitive
// selection. Unlike browsing Sessions, it must propagate read/transport failures
// rather than return cached or partial results, including empty fallbacks.
type SessionInventoryReader interface {
	SessionInventory(context.Context, string) ([]db.Session, error)
}
