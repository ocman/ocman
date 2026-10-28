package conversationingest

import (
	"context"
	"github.com/ocman/ocman/internal/statev2"
)

func conversationRefreshes(sessions []statev2.Session) map[string]bool {
	refresh := map[string]bool{}
	for _, s := range sessions {
		if s.ReadFormat != statev2.ConversationReadFormat {
			refresh[s.SessionID] = true
		}
	}
	return refresh
}

// Read receipts are separate from scan hints: a hint may be remembered before
// the accounting check finishes, but content is not reader-ready until checked.
func (w *sourceWorker) clearReadReceipt(ctx context.Context, id string) error {
	return w.module.deps.Store.SetSourceState(ctx, w.instance.ID, "read-ready/"+id, "")
}

func (w *sourceWorker) approveRead(ctx context.Context, id, version string, tickets ...uint64) error {
	if err := w.module.deps.Store.SetSourceState(ctx, w.instance.ID, "read-ready/"+id, version); err != nil {
		return err
	}
	w.module.publishReceipt(w.source.AdapterID, id, tickets...)
	w.completePriority(id)
	return nil
}
