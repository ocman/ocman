package platforms

import (
	"context"
	"github.com/NoUseFreak/ocman/internal/db"
)

// ReloadIdleReader provides authoritative, fail-closed process status when the
// browsing list is deliberately best-effort across multiple supervisors.
type ReloadIdleReader interface {
	ReloadIdle(context.Context) (bool, error)
}

func ReloadSessionActive(session db.Session) bool {
	// Waiting also means a completed, unread assistant answer. Only running
	// turns, quota holds and explicit pending prompts keep reload fenced.
	return session.Status == db.StatusBusy || session.Status == db.StatusLimited || session.PendingPermission || session.PendingQuestion
}
