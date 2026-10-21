package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Legacy calculations remain test references for parity and query unit tests,
// not a source selector available to production routes.
type calculationRouter struct {
	muxRouter
	module *Module
	source *db.DB
}

func (r calculationRouter) Handle(path string, h http.HandlerFunc) {
	m := r.module
	source := r.source
	reference := map[string]http.HandlerFunc{
		"/api/stats": m.handleStats, "/api/activity": m.handleActivity, "/api/models": m.handleModels,
		"/api/hourly": m.handleHourly, "/api/hourly-tokens": m.handleHourlyTokens,
		"/api/analytics/overview": m.handleAnalyticsOverview, "/api/analytics/session-concurrency": m.handleSessionConcurrency,
		"/api/analytics/agent-run-hours": m.handleAgentRunHours, "/api/metrics": m.handleMetrics,
		"/api/metrics/performance": m.handleMetricsPerformance, "/api/metric-logs": m.handleMetricLogs,
	}
	if next, ok := reference[path]; ok {
		h = method(http.MethodGet, m.deps.Guards.Auth(func(w http.ResponseWriter, r *http.Request) {
			setAnalyticsSource(w.Header(), "legacy", "")
			if source != nil {
				r = r.WithContext(context.WithValue(r.Context(), analyticsSourceKey{}, source))
			}
			next(w, r)
		}))
	}
	r.muxRouter.Handle(path, h)
}

func calculationRoutes(m *Module, source *db.DB) *http.ServeMux {
	mux := http.NewServeMux()
	m.Routes(calculationRouter{muxRouter{mux}, m, source})
	return mux
}

func (f *fixture) serveLegacy(method, target, body string) *httptest.ResponseRecorder {
	return serve(calculationRoutes(f.Module, f.legacy), method, target, body)
}
