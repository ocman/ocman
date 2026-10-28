package owned

import (
	"context"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/conversationread"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func (a *Adapter) waitStored(ctx context.Context, id string) bool {
	if a.historyWait == nil {
		return false
	}
	waitCtx, cancel := context.WithTimeout(ctx, platforms.StoredHistoryWait)
	defer cancel()
	return a.historyWait(waitCtx, id)
}

func (a *Adapter) readStoredConversation(ctx context.Context, id string, limit, offset int) (*statev2.Detail, error) {
	key, err := a.storedKey(ctx, id)
	if err != nil {
		return nil, err
	}
	page, err := a.history.ReadConversation(ctx, key, limit, offset)
	if errors.Is(err, statev2.ErrNotFound) {
		return nil, platforms.ErrHistoryUnavailable
	}
	if err != nil {
		return nil, err
	}
	// Metadata-only upgrades must not strand retained content whose source is gone.
	if !statev2.ReadableConversationFormat(page.Session.ReadFormat) {
		return nil, platforms.ErrHistoryUnavailable
	}
	return page, nil
}

func (a *Adapter) storedConversation(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	if limit < 0 || offset < 0 {
		return nil, fmt.Errorf("stored conversation paging must be nonnegative")
	}
	page, err := a.readStoredConversation(ctx, id, limit, offset)
	if errors.Is(err, platforms.ErrHistoryUnavailable) && a.waitStored(ctx, id) {
		page, err = a.readStoredConversation(ctx, id, limit, offset)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	row, err := a.storedMetadata(ctx, id)
	if err != nil {
		return nil, err
	}
	row.Status = conversationread.InferredStatus(statev2.SessionSummary{Session: page.Session, Totals: page.ConversationTotals[id]})
	observations, err := a.conversationObservations(ctx)
	if err != nil {
		return nil, err
	}
	a.overlayStoredObservation(row, observations)
	// Directory warnings are current observations, so only the live
	// physical source contributes them.
	directory := ""
	if identity, err := a.storedIdentity(ctx, id); err == nil && identity.SourceKey == a.source.SourceKey && a.unavailable == nil {
		directory = row.Directory
	}
	head := *a.storedConfig(directory)
	head.Session = row
	detail, err := conversationread.Convert(page, &head, string(a.ID()))
	if err != nil {
		return nil, err
	}
	for i := range detail.SessionTree {
		if detail.SessionTree[i].ID == id {
			detail.SessionTree[i] = *detail.Session
			continue
		}
		row, err := a.storedMetadata(ctx, detail.SessionTree[i].ID)
		if err != nil {
			return nil, err
		}
		detail.SessionTree[i].RoutineID = row.RoutineID
		detail.SessionTree[i].FactoryAttemptID = row.FactoryAttemptID
		a.overlayStoredObservation(&detail.SessionTree[i], observations)
	}
	return detail, nil
}

// The conversation already contains totals and hierarchy. Its header needs
// only identity metadata and memory-only observations, not another summary pass.
func (a *Adapter) overlayStoredObservation(row *db.Session, lookup func(string) *platforms.LiveState) {
	native, ok := a.byID.Load(row.ID)
	if !ok {
		platforms.OverlaySessionObservation(row, nil, false)
		return
	}
	live := lookup(native.(string))
	if live != nil {
		row.ResetsAt = live.ResetsAt
	}
	// A current source can adopt an idle external session on first send. Lack
	// of a running observation must not make that historical session read-only.
	connected := a.unavailable == nil
	if source, ok := a.native.(platforms.CachedSessionAvailability); ok {
		connected = connected && source.CachedSessionAvailable()
	}
	platforms.OverlaySessionObservation(row, live, connected)
}
