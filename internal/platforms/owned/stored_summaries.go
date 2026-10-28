package owned

import (
	"cmp"
	"context"
	"slices"

	"github.com/ocman/ocman/internal/conversationread"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
)

func (a *Adapter) storedSessions(ctx context.Context, directory, id string) ([]db.Session, error) {
	instances, err := a.storedReadInstances(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []db.Session{}
	for _, instance := range instances {
		rows, err := a.storedInstanceSessions(ctx, instance, directory, id)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	slices.SortStableFunc(out, func(a, b db.Session) int {
		if c := cmp.Compare(b.TimeUpdated, a.TimeUpdated); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (a *Adapter) storedInstanceSessions(ctx context.Context, instance statev2.Instance, directory, id string) ([]db.Session, error) {
	summaries, err := a.history.SessionSummaries(ctx, instance.ID, directory, id)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if id != "" || !summary.Subagent {
			ids = append(ids, summary.SessionID)
		}
	}
	identities, err := a.storedIdentities(ctx, ids)
	if err != nil {
		return nil, err
	}
	config := a.storedConfig("")
	rows := make([]db.Session, 0, len(summaries))
	native := make([]db.Session, 0, len(summaries))
	for _, summary := range summaries {
		if id == "" && summary.Subagent {
			continue
		}
		identity, ok := identities[summary.SessionID]
		if !ok {
			continue
		}
		if identity.SourceKey != instance.SourceKey {
			return nil, platforms.ErrHistoryUnavailable
		}
		row := conversationread.Summary(summary, string(a.ID()), config)
		if summary.ReadFormat > 0 && summary.ReadFormat < statev2.SourceHierarchyReadFormat {
			row.SourceChild = identity.NativeParentID != ""
		}
		if identity.Origin == sessioncatalog.Routine {
			row.RoutineID = identity.OriginID
		}
		rows = append(rows, row)
		row.ID = identity.NativeID
		row.Status = conversationread.InferredStatus(summary)
		native = append(native, row)
	}
	// Offline history remains readable, but stale observations never become live
	// execution signals after a source replacement or failed authentication.
	if instance.SourceKey != a.source.SourceKey {
		return rows, nil
	}
	background, err := a.backgroundSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	for i, row := range native {
		bg := background[row.ID]
		rows[i].BackgroundTaskCount, rows[i].BackgroundServiceCount = bg.Count, serviceCount(bg)
	}
	if err := a.checkSource(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return rows, nil
	}
	if enricher, ok := a.native.(platforms.StoredSessionEnricher); ok {
		parents, err := a.storedNativeParents(ctx, instance)
		if err != nil {
			return nil, err
		}
		if err := enricher.EnrichStoredSessions(ctx, native, parents); err != nil {
			return nil, err
		}
	} else {
		for i := range native {
			a.enrichObservedStatus(&native[i])
		}
	}
	for i, observation := range native {
		rows[i].Status, rows[i].LiveConnection = observation.Status, observation.LiveConnection
		rows[i].PendingPermission, rows[i].PendingQuestion = observation.PendingPermission, observation.PendingQuestion
		rows[i].Notice, rows[i].ResetsAt, rows[i].LastHaltAt = observation.Notice, observation.ResetsAt, observation.LastHaltAt
	}
	return rows, nil
}

func (a *Adapter) storedNativeParents(ctx context.Context, instance statev2.Instance) (map[string]string, error) {
	parents, err := a.history.SessionParents(ctx, instance.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	ids := make([]string, 0, len(parents))
	for id := range parents {
		ids = append(ids, id)
	}
	identities, err := a.storedIdentities(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range identities {
		if row.SourceKey != instance.SourceKey {
			return nil, platforms.ErrHistoryUnavailable
		}
		out[row.NativeID] = row.NativeParentID
	}
	return out, nil
}

func (a *Adapter) enrichObservedStatus(row *db.Session) {
	observed := a.native.LiveStatus(row.ID)
	platforms.OverlaySessionObservation(row, observed, observed != nil)
}

func (a *Adapter) storedSummary(ctx context.Context, id string) (*db.Session, error) {
	rows, err := a.storedSessions(ctx, "", id)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, platforms.ErrNotFound
	}
	return &rows[0], nil
}

func filterStoredSessions(rows []db.Session, since int64) []db.Session {
	return slices.DeleteFunc(rows, func(row db.Session) bool {
		return (row.SourceChild && row.Status != db.StatusBusy) || (since > 0 && row.TimeUpdated < since && !row.PendingPermission && !row.PendingQuestion)
	})
}
