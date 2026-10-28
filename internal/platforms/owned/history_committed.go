package owned

import (
	"context"
	"math"

	"github.com/ocman/ocman/internal/conversationread"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) HistoryCommitted(ctx context.Context, id string, snapshot *db.HistorySnapshot) error {
	observer, ok := a.native.(platforms.HistoryCommitObserver)
	if !ok {
		return nil
	}
	native, err := a.nativeID(ctx, id)
	if err != nil {
		return err
	}
	// Replayed empty deltas can follow a failed receipt or dashboard restart.
	// Confirm durable data, never a newer uncommitted read from the source.
	if snapshot != nil && snapshot.Subagent && snapshot.Partial && len(snapshot.Messages) == 0 && len(snapshot.Parts) == 0 {
		key, err := a.storedKey(ctx, id)
		if err != nil {
			return err
		}
		page, err := a.history.ReadConversation(ctx, key, math.MaxInt, 0)
		if err != nil {
			return err
		}
		detail, err := conversationread.Convert(page, &platforms.ConversationHead{}, string(a.ID()))
		if err != nil {
			return err
		}
		copy := *snapshot
		copy.Messages, copy.Parts = detail.Messages, detail.Parts
		snapshot = &copy
	}
	return observer.HistoryCommitted(ctx, native, snapshot)
}
