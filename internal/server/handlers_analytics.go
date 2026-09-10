package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/state"
)

type analyticsOverview struct {
	InventoryScope   string `json:"inventoryScope"`
	TotalSessions    int    `json:"totalSessions"`
	SubagentSessions int    `json:"subagentSessions"`
	TotalProjects    int    `json:"totalProjects"`
	state.AnalyticsOverviewCounts
}

func (s *Server) handleAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	stats, err := s.db.GetStats(r.Context())
	if err != nil {
		serverError(w, "fetching analytics session totals", err)
		return
	}
	counts, err := s.stateDB.AnalyticsOverviewCounts(r.Context())
	if err != nil {
		serverError(w, "fetching analytics overview counts", err)
		return
	}
	writeJSON(w, analyticsOverview{
		InventoryScope:          "local",
		TotalSessions:           stats.TotalSessions,
		SubagentSessions:        stats.SubagentSessions,
		TotalProjects:           stats.TotalProjects,
		AnalyticsOverviewCounts: counts,
	})
}
