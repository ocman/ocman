package analytics

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
)

// statsRefreshInterval bounds database work independently of metric exports.
const statsRefreshInterval = 2 * time.Minute

// runStatsRefreshLoop computes the top-line stats on statsRefreshInterval
// for the gauges registered by registerStatsMetrics. It computes the first
// value immediately, then on every tick, in this single goroutine — so
// computations never overlap and the OTel export path does no database work.
// It returns when ctx ends.
func (m *Module) runStatsRefreshLoop(ctx context.Context) {
	if m.deps.HistoryAnalytics == nil && m.getStats == nil || ctx.Err() != nil {
		return
	}
	interval := m.statsRefreshEvery
	if interval <= 0 {
		interval = statsRefreshInterval
	}
	module.Every(ctx, "stats-metrics", interval, m.refreshStats)
}

// refreshStats computes the stats once and keeps the last good values on
// failure (the gauges then keep observing the previous snapshot).
func (m *Module) refreshStats(ctx context.Context) {
	getStats := m.getStats
	if getStats == nil {
		getStats = func(ctx context.Context) (stats *db.Stats, err error) {
			err = m.withStoredAnalytics(ctx, func(reader *db.DB) error { stats, err = reader.GetStats(ctx); return err })
			return stats, err
		}
	}
	stats, err := getStats(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.WithError(err).Warn("stats metrics: failed to query stats")
		}
		return
	}
	m.statsMu.Lock()
	m.statsSnapshot = stats
	m.statsMu.Unlock()
}

func (m *Module) cachedStats() *db.Stats {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return m.statsSnapshot
}

// registerStatsMetrics creates observable gauges for the top-line stats
// from statev2 (session/message/project counts, lifetime
// tokens and cost). The callback only observes the last snapshot computed
// by runStatsRefreshLoop — collecting metrics never touches the database.
// Nothing is observed until the first successful computation.
//
// Returns nil registration (and nil error) without a history reader. Callers should
// treat a nil return as "nothing to clean up".
func (m *Module) registerStatsMetrics(ctx context.Context, meter metric.Meter) (metric.Registration, error) {
	if m.deps.HistoryAnalytics == nil && m.getStats == nil {
		return nil, nil
	}

	gauges := []struct {
		name, desc, unit string
		value            func(*db.Stats) int64
	}{
		{"ocman.stats.sessions", "Total number of sessions.", "{session}", func(s *db.Stats) int64 { return int64(s.TotalSessions) }},
		{"ocman.stats.messages", "Total number of user messages.", "{message}", func(s *db.Stats) int64 { return int64(s.TotalMessages) }},
		{"ocman.stats.projects", "Total number of distinct projects.", "{project}", func(s *db.Stats) int64 { return int64(s.TotalProjects) }},
		{"ocman.stats.tokens.input", "Lifetime input tokens across all sessions.", "{token}", func(s *db.Stats) int64 { return s.TotalTokensIn }},
		{"ocman.stats.tokens.output", "Lifetime output tokens across all sessions.", "{token}", func(s *db.Stats) int64 { return s.TotalTokensOut }},
	}
	ints := make([]metric.Int64ObservableGauge, len(gauges))
	instruments := make([]metric.Observable, 0, len(gauges)+1)
	for i, g := range gauges {
		gauge, err := meter.Int64ObservableGauge(g.name, metric.WithDescription(g.desc), metric.WithUnit(g.unit))
		if err != nil {
			return nil, err
		}
		ints[i] = gauge
		instruments = append(instruments, gauge)
	}
	cost, err := meter.Float64ObservableGauge("ocman.stats.cost",
		metric.WithDescription("Lifetime API cost across all sessions."),
		metric.WithUnit("{USD}"),
	)
	if err != nil {
		return nil, err
	}
	instruments = append(instruments, cost)

	requested := make(chan struct{}, 1)
	reg, err := meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			select {
			case requested <- struct{}{}:
			default:
			}
			stats := m.cachedStats()
			if stats == nil {
				return nil // first refresh still pending (or failing); observe nothing
			}
			for i, g := range gauges {
				o.ObserveInt64(ints[i], g.value(stats))
			}
			o.ObserveFloat64(cost, stats.TotalCost)
			return nil
		},
		instruments...,
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		case <-requested:
			m.runStatsRefreshLoop(ctx)
		}
	}()
	return &statsMetricsRegistration{Registration: reg, cancel: cancel, done: done}, nil
}

// Start lazily on the first collection, so disabled telemetry does no DB work.
// Unregister cancels and joins the worker, including on an early server error.
type statsMetricsRegistration struct {
	metric.Registration
	cancel context.CancelFunc
	done   <-chan struct{}
}

func (r *statsMetricsRegistration) Unregister() error {
	r.cancel()
	<-r.done
	return r.Registration.Unregister()
}
