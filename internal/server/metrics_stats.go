package server

import (
	"context"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/metric"
)

// registerStatsMetrics creates observable gauges for the top-line stats
// from the OpenCode database (session/message/project counts, lifetime
// tokens and cost). The gauges are read once per OTel collection
// interval (typically 15–60 s) via a single GetStats() call — the same
// four cheap SQL aggregations that power /api/stats.
//
// Returns nil registration (and nil error) when s.db is nil, which
// happens when the OpenCode platform is not enabled. Callers should
// treat a nil return as "nothing to clean up".
func (s *Server) registerStatsMetrics(meter metric.Meter) (metric.Registration, error) {
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

	return meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			stats, err := s.db.GetStats(ctx)
			if err != nil {
				log.WithError(err).Warn("stats metrics: failed to query stats")
				return nil // don't fail the collection cycle
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
}
