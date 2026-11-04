package autoarchive

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

// InitialImport tidies only a newly imported owner-local history. It does not
// change the separate, configurable inactivity policy or native source data.
func (m *Module) InitialImport(ctx context.Context, platform string, sessions []statev2.Session) error {
	if m.d.Store == nil {
		return nil
	}
	settings, err := m.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return m.d.Store.ArchiveInitialSessions(ctx, platform, nil)
	}
	var adapter platforms.Platform
	for _, p := range m.d.Platforms() {
		if string(p.ID()) == platform {
			adapter = p
			break
		}
	}
	if adapter == nil || !adapter.Available(ctx) {
		return fmt.Errorf("initial import platform %q unavailable", platform)
	}
	// Hidden children and deleted sources must not consume the twenty slots.
	rows := make([]statev2.Session, 0, len(sessions))
	for _, s := range sessions {
		if !s.SourceDeleted && !s.Subagent && s.ParentID == "" {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].SessionID < rows[j].SessionID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	liveState := adapter.LiveStatus
	if reader, ok := adapter.(platforms.SessionObservationReader); ok {
		ids := make([]string, 0, len(rows))
		for _, s := range rows {
			ids = append(ids, s.SessionID)
		}
		observations, err := reader.SessionObservations(ctx, ids)
		if err != nil {
			return err
		}
		liveState = func(id string) *platforms.LiveState { v := observations[id]; return &v }
	}
	var candidates []state.InitialArchiveSession
	for i, s := range rows {
		if i < 20 || s.UpdatedAt <= 0 || s.UpdatedAt >= cutoff {
			continue
		}
		if live := liveState(s.SessionID); live != nil && (live.Status == "busy" || live.Status == "waiting" || live.PendingPermission || live.PendingQuestion || live.BackgroundTaskCount > 0 || live.BackgroundServiceCount > 0) {
			continue
		}
		candidates = append(candidates, state.InitialArchiveSession{ID: s.SessionID, TimeUpdated: s.UpdatedAt})
	}
	return m.d.Store.ArchiveInitialSessions(ctx, platform, candidates)
}
