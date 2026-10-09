package telemetry

import (
	"context"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/metric"
)

// RecordUIUsage exports only newly committed time, with no client/user/date
// attributes. SQLite owns the durable history; this counter is process-local.
func RecordUIUsage(ctx context.Context, milliseconds int64) {
	if milliseconds <= 0 {
		return
	}
	counter, err := Meter().Float64Counter("ocman.ui.active_time",
		metric.WithUnit("s"), metric.WithDescription("Foreground UI time, excluding inactivity longer than five minutes."))
	if err != nil {
		log.WithError(err).Warn("creating UI active time counter")
		return
	}
	counter.Add(ctx, float64(milliseconds)/1000)
}
