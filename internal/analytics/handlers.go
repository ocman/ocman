package analytics

import (
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (m *Module) handleSessionConcurrency(w http.ResponseWriter, r *http.Request) {
	if !m.requireRuntimeAnalytics(w, r) {
		return
	}
	data, err := m.runtimeAnalyticsReader(r.Context()).GetSessionConcurrency(r.Context(), sinceParam(r), time.Now().UnixMilli(), dirParam(r))
	if err != nil {
		httpx.ServerError(w, "fetching session concurrency", err)
		return
	}
	httpx.WriteJSON(w, data)
}

func (m *Module) handleAgentRunHours(w http.ResponseWriter, r *http.Request) {
	if !m.requireRuntimeAnalytics(w, r) {
		return
	}
	since, until := sinceParam(r), time.Now().UnixMilli()
	waits := make(map[string][]db.RunInterval)
	if m.deps.Store != nil {
		observed, err := m.deps.Store.AgentUserWaits(r.Context(), string(opencode.PlatformID), since, until)
		if err != nil {
			httpx.ServerError(w, "reading permission waits", err)
			return
		}
		for _, wait := range observed {
			waits[wait.SessionID] = append(waits[wait.SessionID], db.RunInterval{Start: wait.Start, End: wait.End})
		}
	}
	data, err := m.runtimeAnalyticsReader(r.Context()).GetAgentRunHours(r.Context(), since, until, dirParam(r), waits)
	if err != nil {
		httpx.ServerError(w, "reading agent run hours", err)
		return
	}
	httpx.WriteJSON(w, data)
}

type analyticsOverview struct {
	InventoryScope   string `json:"inventoryScope"`
	TotalSessions    int    `json:"totalSessions"`
	SubagentSessions int    `json:"subagentSessions"`
	TotalProjects    int    `json:"totalProjects"`
	state.AnalyticsOverviewCounts
}

func (m *Module) handleAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w, r) {
		return
	}
	stats, err := m.analyticsReader(r.Context()).GetStats(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching analytics session totals", err)
		return
	}
	counts, err := m.deps.Store.AnalyticsOverviewCounts(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching analytics overview counts", err)
		return
	}
	httpx.WriteJSON(w, analyticsOverview{
		InventoryScope:          "local",
		TotalSessions:           stats.TotalSessions,
		SubagentSessions:        stats.SubagentSessions,
		TotalProjects:           stats.TotalProjects,
		AnalyticsOverviewCounts: counts,
	})
}

func (m *Module) handleDatabaseSizes(w http.ResponseWriter, r *http.Request) {
	if m.deps.Store == nil {
		httpx.WriteJSON(w, []state.DatabaseSizeSample{})
		return
	}
	samples, err := m.deps.Store.DatabaseSizeSamples(r.Context(), sinceParam(r))
	if err != nil {
		httpx.ServerError(w, "fetching database size samples", err)
		return
	}
	if samples == nil {
		samples = []state.DatabaseSizeSample{}
	}
	httpx.WriteJSON(w, samples)
}
