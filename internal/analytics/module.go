// Package analytics is the feature module for usage analytics: the Stats and
// Usage endpoints (/api/stats, /api/metrics*, /api/metric-logs,
// /api/analytics/*, /api/activity, /api/models, /api/hourly*,
// /api/permission-stats, /api/cost/calc), UI-usage recording, subscription
// usage, and the background work behind them: database-size sampling, the
// OTel stats gauges and the LLM-turn scanner.
//
// Queries stay in internal/db (OpenCode, read-only) and internal/state;
// the session snapshot and the server's own OTel instruments (HTTP, SSE,
// auto-archive, projects index) stay in core.
package analytics

import (
	"context"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// Deps is what the server supplies.
type Deps struct {
	// HistoryAnalytics is resolved per request so startup wiring can finish
	// before the module's lazy dependencies are used.
	HistoryAnalytics        func() *HistoryAnalytics
	SubscriptionCredentials func(context.Context) (map[string]db.SubscriptionOAuth, error)
	SessionIdentity         func(context.Context, string, string) (string, error)
	// Store is ocman's state database; may be nil.
	Store *state.DB
	// Version is the build version reported by /api/stats, read per request.
	Version func() string
	// TypeSafeConfigured reports a keyed, hosted reviewer at request time.
	TypeSafeConfigured func(context.Context) (bool, error)
	Guards             module.Guards
}

// Module owns the analytics routes, their caches and refresh workers.
type Module struct {
	deps Deps

	subscription      subscriptionUsageClient
	subscriptionCache subscriptionUsageCache

	// Test seams; nil means the database.
	getNewAssistantMessages func(context.Context, int64) ([]db.LLMMessageRow, int64, error)
	getStats                func(context.Context) (*db.Stats, error)
	statsRefreshEvery       time.Duration

	// statsSnapshot is the last good stats computation, read by the gauges.
	statsMu       sync.Mutex
	statsSnapshot *db.Stats
}

func NewModule(deps Deps) *Module {
	if deps.Version == nil {
		deps.Version = func() string { return "" }
	}
	client := newSubscriptionUsageClient()
	client.credentials = deps.SubscriptionCredentials
	return &Module{deps: deps, subscription: client}
}

func (m *Module) Name() string { return "analytics" }

// Routes keeps each route's method check ahead of its guard, as before the
// extraction.
func (m *Module) Routes(r module.Router) {
	get := func(h http.HandlerFunc) http.HandlerFunc { return method(http.MethodGet, m.deps.Guards.Auth(h)) }
	data := func(h http.HandlerFunc) http.HandlerFunc { return get(m.analyticsRoute(h)) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return method(http.MethodPost, m.deps.Guards.Auth(h)) }
	r.Handle("/api/stats", data(m.handleStats))
	r.Handle("/api/analytics/overview", data(m.handleAnalyticsOverview))
	r.Handle("/api/analytics/history-health", get(m.handleHistoryHealth))
	r.Handle("/api/analytics/session-concurrency", data(m.handleSessionConcurrency))
	r.Handle("/api/analytics/database-sizes", get(m.handleDatabaseSizes))
	r.Handle("/api/analytics/ui-usage", get(m.handleUIUsageDays))
	r.Handle("/api/analytics/agent-run-hours", data(m.handleAgentRunHours))
	r.Handle("/api/ui-usage", post(m.handleUIUsage))
	r.Handle("/api/subscription-usage", method(http.MethodGet, m.deps.Guards.Localhost(m.handleSubscriptionUsage)))
	r.Handle("/api/provider-usage", method(http.MethodGet, m.deps.Guards.Localhost(m.handleSubscriptionUsage)))
	r.Handle("/api/metrics", data(m.handleMetrics))
	r.Handle("/api/metrics/performance", data(m.handleMetricsPerformance))
	r.Handle("/api/metric-logs", data(m.handleMetricLogs))
	r.Handle("/api/permission-stats", get(m.handlePermissionStats))
	r.Handle("/api/activity", data(m.handleActivity))
	r.Handle("/api/models", data(m.handleModels))
	r.Handle("/api/hourly", data(m.handleHourly))
	r.Handle("/api/hourly-tokens", data(m.handleHourlyTokens))
	r.Handle("/api/cost/calc", post(m.handleCalcCost))
}

// Run samples database sizes, scans LLM turns (only with telemetry on) and
// serves the stats gauges, which refresh lazily on the first collection.
// It returns once every worker has, so no query outlives the databases.
func (m *Module) Run(ctx context.Context) {
	if reg, err := m.registerStatsMetrics(ctx, telemetry.Meter()); err != nil {
		log.WithError(err).Warn("failed to register stats metrics")
	} else if reg != nil {
		defer func() { _ = reg.Unregister() }()
	}
	var workers sync.WaitGroup
	workers.Go(func() { m.runLLMMetricsLoop(ctx) })
	workers.Go(func() { m.runDatabaseSizeLoop(ctx) })
	workers.Wait()
	<-ctx.Done() // the gauges stay registered for the server's lifetime
}

func method(want string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != want {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// requireAnalytics rejects a missing scoped reader. Routes normally reject it
// in analyticsRoute before entering the handler.
func (m *Module) requireAnalytics(w http.ResponseWriter, r *http.Request) bool {
	if m.analyticsReader(r.Context()) == nil {
		http.Error(w, "conversation analytics unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// runWithRecover keeps one panicking tick from stopping its loop.
func runWithRecover(name string, body func()) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"loop": name, "panic": r, "stack": string(debug.Stack())}).
				Error("background loop panicked, continuing on next tick")
		}
	}()
	body()
}

// intParam reads an integer query parameter, or fallback when it is absent
// or invalid.
func intParam(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil {
		return fallback
	}
	return n
}

// sinceParam turns ?days= into a Unix-millisecond cutoff; 0 means no filter.
func sinceParam(r *http.Request) int64 {
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" && v != "0" {
		dayCount, err := strconv.ParseInt(v, 10, 64)
		if err == nil && dayCount > 0 {
			return time.Now().Add(-time.Duration(dayCount) * 24 * time.Hour).UnixMilli()
		}
	}
	return 0
}

// dirParam trims whitespace and one trailing slash from a ?dir= prefix filter.
func dirParam(r *http.Request) string {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if len(dir) > 1 {
		dir = strings.TrimSuffix(dir, "/")
	}
	return dir
}
