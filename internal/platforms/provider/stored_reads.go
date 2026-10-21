package provider

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) EnrichStoredSessions(ctx context.Context, rows []db.Session, parents map[string]string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	connected := !a.draining && (a.ctx == nil || a.ctx.Err() == nil)
	if !connected {
		for i := range rows {
			platforms.OverlaySessionObservation(&rows[i], nil, false)
		}
		return nil
	}
	permissions, questions := map[string]bool{}, map[string]bool{}
	a.mu.RLock()
	for _, p := range a.permissions {
		if id, ok := p["sessionID"].(string); ok {
			permissions[id] = true
		}
	}
	for _, p := range a.questions {
		if id, ok := p["sessionID"].(string); ok {
			questions[id] = true
		}
	}
	a.mu.RUnlock()
	permissions = platforms.BubblePromptSessions(permissions, parents)
	questions = platforms.BubblePromptSessions(questions, parents)
	for i := range rows {
		row := &rows[i]
		live := a.LiveStatus(row.ID)
		platforms.OverlaySessionObservation(row, live, connected)
		row.PendingPermission = row.PendingPermission || permissions[row.ID]
		row.PendingQuestion = row.PendingQuestion || questions[row.ID]
		if row.Status == db.StatusBusy && (row.PendingPermission || row.PendingQuestion) {
			row.Status = db.StatusWaiting
		}
	}
	return nil
}
