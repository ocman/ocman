package platforms

import (
	"context"
	"fmt"
)

// SessionCreationCommitter keeps platform allocation/configuration behind the
// canonical interface while application-owned linkage completes before publish.
type SessionCreationCommitter interface {
	CommitCreation(context.Context, string) error
}

// CreationPendingError retains only the canonical recovery identity. It never
// authorizes retrying an allocation or deleting its worktree.
type CreationPendingError struct {
	SessionID string
	Err       error
}

func (e *CreationPendingError) Error() string {
	return fmt.Sprintf("session %s requires creation reconciliation: %v", e.SessionID, e.Err)
}
func (e *CreationPendingError) Unwrap() error { return e.Err }
