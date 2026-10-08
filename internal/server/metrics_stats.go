package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/db"
)

// statsRefreshInterval bounds database work independently of metric exports.
const statsRefreshInterval = 2 * time.Minute

// runStatsRefreshLoop computes the top-line stats on statsRefreshInterval
// for the gauges registered by registerStatsMetrics. It computes the first
// value immediately, then on every tick, in this single goroutine — so
// computations never overlap and the OTel export path does no database work.
// It returns when ctx ends.
func (s *Server) runStatsRefreshLoop(ctx context.Context) {
	if s.db == nil || ctx.Err() != nil {
		return
	}
	interval := s.statsRefreshEvery
	if interval <= 0 {
		interval = statsRefreshInterval
	}
	tick := func() { s.refreshStats(ctx) }
	runWithRecover("stats-metrics", tick)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			runWithRecover("stats-metrics", tick)
			ticker.Reset(interval) // no queued catch-up refresh after a slow query
		}
	}
}

// refreshStats computes the stats once and keeps the last good values on
// failure (the gauges then keep observing the previous snapshot).
func (s *Server) refreshStats(ctx context.Context) {
	getStats := s.getStats
	if getStats == nil {
		getStats = s.db.GetStats
	}
	stats, err := getStats(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.WithError(err).Warn("stats metrics: failed to query stats")
		}
		return
	}
	s.statsMu.Lock()
	s.statsSnapshot = stats
	s.statsMu.Unlock()
}

func (s *Server) cachedStats() *db.Stats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.statsSnapshot
}

// registerStatsMetrics creates observable gauges for the top-line stats
// from the OpenCode database (session/message/project counts, lifetime
// tokens and cost). The callback only observes the last snapshot computed
// by runStatsRefreshLoop — collecting metrics never touches the database.
// Nothing is observed until the first successful computation.
//
// Returns nil registration (and nil error) when s.db is nil, which
// happens when the OpenCode platform is not enabled. Callers should
// treat a nil return as "nothing to clean up".
func (s *Server) registerStatsMetrics(ctx context.Context, meter metric.Meter) (metric.Registration, error) {
	if s.db == nil {
		return nil, nil
	}

	sessions, err := meter.Int64ObservableGauge("ocman.stats.sessions",
		metric.WithDescription("Total number of sessions."),
		metric.WithUnit("{session}"),
	)
	if err != nil {
		return nil, err
	}

	messages, err := meter.Int64ObservableGauge("ocman.stats.messages",
		metric.WithDescription("Total number of user messages."),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, err
	}

	projects, err := meter.Int64ObservableGauge("ocman.stats.projects",
		metric.WithDescription("Total number of distinct projects."),
		metric.WithUnit("{project}"),
	)
	if err != nil {
		return nil, err
	}

	tokensIn, err := meter.Int64ObservableGauge("ocman.stats.tokens.input",
		metric.WithDescription("Lifetime input tokens across all sessions."),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, err
	}

	tokensOut, err := meter.Int64ObservableGauge("ocman.stats.tokens.output",
		metric.WithDescription("Lifetime output tokens across all sessions."),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, err
	}

	cost, err := meter.Float64ObservableGauge("ocman.stats.cost",
		metric.WithDescription("Lifetime API cost across all sessions."),
		metric.WithUnit("{USD}"),
	)
	if err != nil {
		return nil, err
	}

	requested := make(chan struct{}, 1)
	reg, err := meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			select {
			case requested <- struct{}{}:
			default:
			}
			stats := s.cachedStats()
			if stats == nil {
				return nil // first refresh still pending (or failing); observe nothing
			}
			o.ObserveInt64(sessions, int64(stats.TotalSessions))
			o.ObserveInt64(messages, int64(stats.TotalMessages))
			o.ObserveInt64(projects, int64(stats.TotalProjects))
			o.ObserveInt64(tokensIn, stats.TotalTokensIn)
			o.ObserveInt64(tokensOut, stats.TotalTokensOut)
			o.ObserveFloat64(cost, stats.TotalCost)
			return nil
		},
		sessions, messages, projects, tokensIn, tokensOut, cost,
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
			s.runStatsRefreshLoop(ctx)
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
