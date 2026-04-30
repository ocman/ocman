package server

import (
	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// Server-side custom OTel instruments. These complement otelhttp's
// auto-collected http.server.* metrics and otelsql's db.client.*
// metrics with ocman-specific business signals: archive throughput,
// projects-index refresh latency, and the live SSE connection count.
//
// Instruments are package-level globals and lazily created on first
// reference (each metric.Meter call returns the same handle for the
// same name). They're created once at init() time so the cost of
// creation doesn't show up in request-handler hot paths.
//
// All instruments degrade to no-ops when telemetry is disabled, so
// the call sites can record unconditionally.

var (
	autoArchiveSessions          metric.Int64Counter
	autoArchiveRuns              metric.Int64Counter
	projectsIndexRefreshDuration metric.Float64Histogram
	projectsIndexRefreshErrors   metric.Int64Counter
	sseActiveConnections         metric.Int64UpDownCounter
)

func init() {
	meter := telemetry.Meter()

	var err error
	if autoArchiveSessions, err = meter.Int64Counter(
		"ocman.auto_archive.sessions",
		metric.WithDescription("Sessions archived by the auto-archive background loop."),
		metric.WithUnit("{session}"),
	); err != nil {
		log.WithError(err).Warn("creating auto_archive.sessions counter")
	}

	if autoArchiveRuns, err = meter.Int64Counter(
		"ocman.auto_archive.runs",
		metric.WithDescription("Auto-archive loop iterations."),
		metric.WithUnit("{run}"),
	); err != nil {
		log.WithError(err).Warn("creating auto_archive.runs counter")
	}

	if projectsIndexRefreshDuration, err = meter.Float64Histogram(
		"ocman.projects_index.refresh.duration",
		metric.WithDescription("Time spent refreshing the projects index."),
		metric.WithUnit("ms"),
	); err != nil {
		log.WithError(err).Warn("creating projects_index.refresh.duration histogram")
	}

	if projectsIndexRefreshErrors, err = meter.Int64Counter(
		"ocman.projects_index.refresh.errors",
		metric.WithDescription("Failed projects-index refreshes."),
		metric.WithUnit("{error}"),
	); err != nil {
		log.WithError(err).Warn("creating projects_index.refresh.errors counter")
	}

	if sseActiveConnections, err = meter.Int64UpDownCounter(
		"ocman.sse.active_connections",
		metric.WithDescription("Currently open SSE event-stream connections."),
		metric.WithUnit("{connection}"),
	); err != nil {
		log.WithError(err).Warn("creating sse.active_connections updowncounter")
	}
}
