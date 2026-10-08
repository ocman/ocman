package server

import (
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Server) handleSessionConcurrency(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	data, err := s.db.GetSessionConcurrency(r.Context(), parseSinceParam(r), time.Now().UnixMilli(), normaliseDirParam(r.URL.Query().Get("dir")))
	if err != nil {
		serverError(w, "fetching session concurrency", err)
		return
	}
	writeJSON(w, data)
}

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

func (s *Server) handleDatabaseSizes(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		writeJSON(w, []state.DatabaseSizeSample{})
		return
	}
	samples, err := s.stateDB.DatabaseSizeSamples(r.Context(), parseSinceParam(r))
	if err != nil {
		serverError(w, "fetching database size samples", err)
		return
	}
	if samples == nil {
		samples = []state.DatabaseSizeSample{}
	}
	writeJSON(w, samples)
}
