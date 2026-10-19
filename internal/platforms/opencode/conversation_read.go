package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) ConversationHead(ctx context.Context, id string) (*platforms.ConversationHead, error) {
	identity, err := a.HistoryIdentity(ctx)
	if err != nil {
		return nil, err
	}
	h := &platforms.ConversationHead{Identity: identity, DirectoryDefaults: true, Pricing: a.pricing}
	row, err := a.db.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return h, platforms.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	lifecycle, err := a.SessionLifecycle(ctx, id)
	if err != nil {
		return nil, err
	}
	row.Status = lifecycle.Status
	row.Platform = string(a.ID())
	ports := a.rt.discoverOpenCodePorts()
	row.LiveConnection = a.rt.directoryHasLivePort(ports, row.Directory)
	if live := a.LiveStatus(id); live != nil {
		row.PendingPermission, row.PendingQuestion = live.PendingPermission, live.PendingQuestion
	}
	h.Session = row
	h.Version = fmt.Sprint(row.TimeUpdated)
	if a.db.V2() {
		// v2 session timestamps miss message changes; the durable event
		// sequence is the version history ingestion records with its receipts.
		if h.Version, err = a.db.HistorySequence(ctx, id); err != nil {
			return nil, err
		}
	}
	h.Warnings = a.rt.sessionWarningsForDirectory(row.Directory)
	return h, nil
}
