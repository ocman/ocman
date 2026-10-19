package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
)

// Legacy calculations remain test references for parity and query unit tests,
// not a source selector available to production routes.
type calculationRouter struct {
	muxRouter
	module *Module
}

func (r calculationRouter) Handle(path string, h http.HandlerFunc) {
	m := r.module
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
			if m.deps.Analytics != nil {
				r = r.WithContext(context.WithValue(r.Context(), analyticsSourceKey{}, m.deps.Analytics))
			}
			next(w, r)
		}))
	}
	r.muxRouter.Handle(path, h)
}

func calculationRoutes(m *Module) *http.ServeMux {
	mux := http.NewServeMux()
	m.Routes(calculationRouter{muxRouter{mux}, m})
	return mux
}

func (f *fixture) serveLegacy(method, target, body string) *httptest.ResponseRecorder {
	return serve(calculationRoutes(f.Module), method, target, body)
}
