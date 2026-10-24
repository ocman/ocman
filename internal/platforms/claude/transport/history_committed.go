package claudecode

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider"
)

func (p *claudePlatform) HistoryCommitted(ctx context.Context, id string, snapshot *db.HistorySnapshot) error {
	if snapshot == nil || !snapshot.Subagent || p.projects == nil {
		return nil
	}
	receipt := claudeprovider.HistoryConfirmation(snapshot)
	var cause error
	for _, adapter := range p.projects.adapters() {
		// Knows selects candidates only. Each supervisor verifies it owns the
		// child and matches committed content before releasing any memory.
		if adapter.SupportsHistoryConfirmation() && adapter.Knows(id) {
			cause = errors.Join(cause, adapter.ConfirmHistory(ctx, id, receipt))
		}
	}
	return cause
}
