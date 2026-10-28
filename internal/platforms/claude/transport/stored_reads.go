package claudecode

import (
	"context"
	"slices"

	"github.com/ocman/ocman/internal/db"
)

// EnrichStoredSessions keeps the existing supervisor status authority without
// enumerating transcript-backed sessions. LiveSessions contains only runtime
// status/reset observations; persisted metadata always remains statev2-owned.
func (p *claudePlatform) EnrichStoredSessions(ctx context.Context, rows []db.Session, parents map[string]string) error {
	if err := p.Adapter.EnrichStoredSessions(ctx, rows, parents); err != nil {
		return err
	}
	settled := map[string]db.Session{}
	for _, adapter := range p.projects.adapters() {
		live, err := adapter.LiveSessions(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		for _, row := range live {
			settled[row.ID] = row
		}
		observations := slices.Clone(rows)
		if err := adapter.EnrichStoredSessions(ctx, observations, parents); err != nil {
			return err
		}
		for i, observation := range observations {
			rows[i].PendingPermission = rows[i].PendingPermission || observation.PendingPermission
			rows[i].PendingQuestion = rows[i].PendingQuestion || observation.PendingQuestion
		}
	}
	for i := range rows {
		row := &rows[i]
		if live, ok := settled[row.ID]; ok {
			row.Status, row.ResetsAt = live.Status, live.ResetsAt
		}
		if adapter := p.projects.owner(row.ID); adapter != nil {
			if live := adapter.LiveStatus(row.ID); live != nil {
				row.PendingPermission, row.PendingQuestion = row.PendingPermission || live.PendingPermission, row.PendingQuestion || live.PendingQuestion
			}
		}
		if row.Status == db.StatusBusy && (row.PendingPermission || row.PendingQuestion) {
			row.Status = db.StatusWaiting
		}
	}
	return nil
}
