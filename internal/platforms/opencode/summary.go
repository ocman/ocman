package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) SessionSummary(ctx context.Context, id string) (*db.Session, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	row, err := a.db.GetSessionSummary(ctx, id)
	if err != nil {
		return nil, err
	}
	row.Platform = string(a.ID())
	port := portForDirectory(discoverOpenCodePorts(), row.Directory)
	row.Status = a.settleStatusOnPort(id, port, row.Status)
	row.Notice = a.sessionNoticeOnPort(id, port)
	row.LiveConnection = port != ""
	permissions, questions := a.prompts.pendingSessionIDs()
	row.PendingPermission = bubbleUpPromptsToParent(ctx, permissions, a.db)[id]
	row.PendingQuestion = bubbleUpPromptsToParent(ctx, questions, a.db)[id]
	rows := []db.Session{row}
	if err := a.attachSessionHalts(ctx, rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}
