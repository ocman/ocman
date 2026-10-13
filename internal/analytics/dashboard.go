package analytics

import (
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (m *Module) handleStats(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	stats, err := m.deps.Analytics.GetStats(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching stats", err)
		return
	}
	stats.Version = m.deps.Version()
	httpx.WriteJSON(w, stats)
}

func (m *Module) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	metrics, err := m.deps.Analytics.GetMetricsDashboard(r.Context(), metricsOptions(r))
	if err != nil {
		httpx.ServerError(w, "fetching metrics", err)
		return
	}
	httpx.WriteJSON(w, metrics)
}

func (m *Module) handleMetricsPerformance(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	metrics, err := m.deps.Analytics.GetMetricsPerformance(r.Context(), metricsOptions(r))
	if err != nil {
		httpx.ServerError(w, "fetching performance metrics", err)
		return
	}
	httpx.WriteJSON(w, metrics)
}

func (m *Module) handleMetricLogs(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	kind := db.MetricsLogKind(strings.TrimSpace(r.URL.Query().Get("kind")))
	if kind != db.MetricsLogRequests && kind != db.MetricsLogSessions && kind != db.MetricsLogProjects {
		http.Error(w, "kind must be request, session, or project", http.StatusBadRequest)
		return
	}
	logs, err := m.deps.Analytics.GetMetricsLog(r.Context(), metricsOptions(r), kind)
	if err != nil {
		httpx.ServerError(w, "fetching metric logs", err)
		return
	}
	httpx.WriteJSON(w, logs)
}

func metricsOptions(r *http.Request) db.MetricsDashboardOptions {
	dayCount := intParam(r, "days", 0)
	var since int64
	if dayCount > 0 {
		since = time.Now().Add(-time.Duration(dayCount) * 24 * time.Hour).UnixMilli()
	}
	return db.MetricsDashboardOptions{
		AgentFilter:   strings.TrimSpace(r.URL.Query().Get("agent")),
		ModelFilter:   strings.TrimSpace(r.URL.Query().Get("model")),
		Since:         since,
		Days:          dayCount,
		RequestLimit:  intParam(r, "limit", 20),
		RequestOffset: intParam(r, "offset", 0),
		SessionLimit:  intParam(r, "sessionLimit", 20),
		SessionOffset: intParam(r, "sessionOffset", 0),
		ProjectLimit:  intParam(r, "projectLimit", 20),
		ProjectOffset: intParam(r, "projectOffset", 0),
		Pricing:       pricing.Load(),
		Dir:           dirParam(r),
	}
}

// handlePermissionStats folds a worktree ?dir= to its project root, so a
// project's managed worktrees count toward it.
func (m *Module) handlePermissionStats(w http.ResponseWriter, r *http.Request) {
	dir := dirParam(r)
	if dir != "" {
		dir = state.ProjectRootForDirectory(dir)
	}
	stats, err := m.deps.Store.PermissionApprovalStats(r.Context(), sinceParam(r), dir)
	if err != nil {
		httpx.ServerError(w, "fetching permission approval stats", err)
		return
	}
	httpx.WriteJSON(w, stats)
}

func (m *Module) handleActivity(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	activity, err := m.deps.Analytics.GetDailyActivity(r.Context(), sinceParam(r), model, dirParam(r))
	if err != nil {
		httpx.ServerError(w, "fetching activity", err)
		return
	}
	httpx.WriteJSON(w, activity)
}

func (m *Module) handleModels(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	models, err := m.deps.Analytics.GetModelUsage(r.Context(), sinceParam(r), dirParam(r))
	if err != nil {
		httpx.ServerError(w, "fetching model usage", err)
		return
	}
	httpx.WriteJSON(w, models)
}

func (m *Module) handleHourlyTokens(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	data, err := m.deps.Analytics.GetHourlyTokensByModel(r.Context(), intParam(r, "days", 0), sinceParam(r), model, dirParam(r))
	if err != nil {
		httpx.ServerError(w, "fetching hourly tokens by model", err)
		return
	}
	httpx.WriteJSON(w, data)
}

func (m *Module) handleHourly(w http.ResponseWriter, r *http.Request) {
	if !m.requireAnalytics(w) {
		return
	}
	hourly, err := m.deps.Analytics.GetHourlyActivity(r.Context(), sinceParam(r), dirParam(r))
	if err != nil {
		httpx.ServerError(w, "fetching hourly activity", err)
		return
	}
	httpx.WriteJSON(w, hourly)
}

// handleCalcCost prices one turn's tokens from the public pricing table.
func (m *Module) handleCalcCost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelID    string `json:"modelID"`
		Input      int64  `json:"input"`
		Output     int64  `json:"output"`
		CacheRead  int64  `json:"cacheRead"`
		CacheWrite int64  `json:"cacheWrite"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	table := pricing.Load()
	cost := table.CalcCost(req.ModelID, req.Input, req.Output, req.CacheRead, req.CacheWrite)
	price := table.Lookup(req.ModelID)
	known := price.InputPerToken != 0 || price.OutputPerToken != 0
	httpx.WriteJSON(w, map[string]interface{}{
		"cost":  cost,
		"known": known,
	})
}
