package server

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineStats struct {
	TotalRuns       int            `json:"totalRuns"`
	States          map[string]int `json:"states"`
	AverageDuration *float64       `json:"averageDurationMs"`
	TotalCost       float64        `json:"totalCost"`
	TotalEstCost    float64        `json:"totalEstCost"`
	CostSessions    int            `json:"costSessions"`
	MissingSessions int            `json:"missingSessions"`
}

func (s *Server) handleRoutineStats(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.routineSvc.Get(r.Context(), id); err != nil {
		s.writeRoutineError(w, "getting routine", err)
		return
	}
	runs, err := s.stateDB.ListRoutineRuns(r.Context(), id)
	if err != nil {
		s.writeRoutineError(w, "reading routine stats", err)
		return
	}
	stats := summarizeRoutineRuns(r.Context(), runs, func(ctx context.Context, key state.Key) (map[string]platforms.Usage, error) {
		adapter, ok := s.registry.Get(platforms.ID(key.Platform))
		if !ok {
			return nil, platforms.ErrNotFound
		}
		reader, ok := adapter.(platforms.UsageReader)
		if !ok {
			return nil, platforms.ErrUnsupported
		}
		return reader.SessionUsage(ctx, key.SessionID)
	})
	writeJSON(w, stats)
}

// ponytail: scan stored runs on demand; persist billing snapshots if per-run costs are needed.
func summarizeRoutineRuns(ctx context.Context, runs []state.RoutineRun, usage func(context.Context, state.Key) (map[string]platforms.Usage, error)) routineStats {
	stats := routineStats{TotalRuns: len(runs), States: make(map[string]int)}
	var duration int64
	var completed int
	sessions := make(map[state.Key]bool)
	counted := make(map[state.Key]bool)
	for _, run := range runs {
		stats.States[run.State]++
		if run.State != "running" && run.StartedAt > 0 && run.FinishedAt >= run.StartedAt {
			duration += run.FinishedAt - run.StartedAt
			completed++
		}
		key := state.Key{Platform: run.Platform, SessionID: run.SessionID}
		if key.SessionID == "" || sessions[key] {
			continue
		}
		sessions[key] = true
		items, err := usage(ctx, key)
		if err != nil {
			stats.MissingSessions++
			continue
		}
		stats.CostSessions++
		for id, item := range items {
			itemKey := state.Key{Platform: key.Platform, SessionID: id}
			if !counted[itemKey] {
				counted[itemKey] = true
				stats.TotalCost += item.Cost
				stats.TotalEstCost += item.EstCost
			}
		}
	}
	if completed > 0 {
		average := float64(duration) / float64(completed)
		stats.AverageDuration = &average
	}
	return stats
}
