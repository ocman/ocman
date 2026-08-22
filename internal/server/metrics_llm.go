package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// llmMetrics holds the OTel instruments for per-LLM-turn metrics.
// These are synchronous counters and histograms incremented by the
// scanner loop each time new assistant messages appear in the DB.
type llmMetrics struct {
	requests     metric.Int64Counter
	tokensInput  metric.Int64Counter
	tokensOutput metric.Int64Counter
	cacheRead    metric.Int64Counter
	cacheWrite   metric.Int64Counter
	cost         metric.Float64Counter
	calcCost     metric.Float64Counter
	duration     metric.Float64Histogram
}

// newLLMMetrics creates the OTel instruments. Returns nil error when
// telemetry is disabled (the meter returns no-op instruments).
func newLLMMetrics(meter metric.Meter) (*llmMetrics, error) {
	m := &llmMetrics{}
	var err error

	if m.requests, err = meter.Int64Counter("ocman.llm.requests",
		metric.WithDescription("LLM assistant turns processed."),
		metric.WithUnit("{request}"),
	); err != nil {
		return nil, err
	}
	if m.tokensInput, err = meter.Int64Counter("ocman.llm.tokens.input",
		metric.WithDescription("Input tokens consumed by LLM turns."),
		metric.WithUnit("{token}"),
	); err != nil {
		return nil, err
	}
	if m.tokensOutput, err = meter.Int64Counter("ocman.llm.tokens.output",
		metric.WithDescription("Output tokens produced by LLM turns."),
		metric.WithUnit("{token}"),
	); err != nil {
		return nil, err
	}
	if m.cacheRead, err = meter.Int64Counter("ocman.llm.tokens.cache_read",
		metric.WithDescription("Cache-read tokens across LLM turns."),
		metric.WithUnit("{token}"),
	); err != nil {
		return nil, err
	}
	if m.cacheWrite, err = meter.Int64Counter("ocman.llm.tokens.cache_write",
		metric.WithDescription("Cache-write tokens across LLM turns."),
		metric.WithUnit("{token}"),
	); err != nil {
		return nil, err
	}
	if m.cost, err = meter.Float64Counter("ocman.llm.cost",
		metric.WithDescription("Platform-reported API cost of LLM turns."),
		metric.WithUnit("{USD}"),
	); err != nil {
		return nil, err
	}
	if m.calcCost, err = meter.Float64Counter("ocman.llm.calc_cost",
		metric.WithDescription("Estimated API cost calculated from token counts and public pricing."),
		metric.WithUnit("{USD}"),
	); err != nil {
		return nil, err
	}
	if m.duration, err = meter.Float64Histogram("ocman.llm.request.duration",
		metric.WithDescription("LLM turn duration (wall-clock time from request to completion)."),
		metric.WithUnit("s"),
	); err != nil {
		return nil, err
	}
	return m, nil
}

// record emits OTel metrics for a single assistant message row.
// The pricing table is used to compute the estimated cost from token
// counts; pass nil to skip the calc_cost counter.
func (m *llmMetrics) record(ctx context.Context, row db.LLMMessageRow, pt *pricing.Table) {
	// ponytail: session_id is a high-cardinality label (one series per
	// session). Fine for ocman's single-user, self-hosted Prometheus; if a
	// deployment ever has huge session counts, drop it via a metric_relabel
	// rule rather than removing the per-session filter.
	attrs := metric.WithAttributes(
		attribute.String("model", row.Model),
		attribute.String("session_id", row.SessionID),
	)
	reqAttrs := metric.WithAttributes(
		attribute.String("model", row.Model),
		attribute.String("stop_reason", row.StopReason),
		attribute.String("session_id", row.SessionID),
	)

	m.requests.Add(ctx, 1, reqAttrs)
	m.tokensInput.Add(ctx, row.InputTokens, attrs)
	m.tokensOutput.Add(ctx, row.OutputTokens, attrs)
	m.cacheRead.Add(ctx, row.CacheReadTokens, attrs)
	m.cacheWrite.Add(ctx, row.CacheWriteTokens, attrs)
	m.cost.Add(ctx, row.Cost, attrs)
	if pt != nil {
		cc := pt.CalcCost(row.Model, row.InputTokens, row.OutputTokens, row.CacheReadTokens, row.CacheWriteTokens)
		if cc > 0 {
			m.calcCost.Add(ctx, cc, attrs)
		}
	}
	if row.DurationMs > 0 {
		m.duration.Record(ctx, float64(row.DurationMs)/1000.0, attrs)
	}
}

// llmMetricsScanInterval is how often the scanner checks for new messages.
const llmMetricsScanInterval = 30 * time.Second

// runLLMMetricsLoop periodically scans for new assistant messages and
// emits OTel metrics for each. The high-water mark starts at the
// current max message time so only messages arriving after ocman
// starts are counted (avoids a counter spike on restart).
func (s *Server) runLLMMetricsLoop(ctx context.Context) {
	if s.db == nil {
		return
	}

	m, err := newLLMMetrics(telemetry.Meter())
	if err != nil {
		log.WithError(err).Warn("failed to create LLM metrics instruments")
		return
	}

	// Initialise high-water mark to the current max so we don't
	// replay the entire history on startup.
	hwm, err := s.db.GetMaxMessageTime(ctx)
	if err != nil {
		log.WithError(err).Warn("LLM metrics: failed to get initial high-water mark")
		return
	}

	ticker := time.NewTicker(llmMetricsScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("llm-metrics", func() {
				rows, newHWM, err := s.db.GetNewAssistantMessages(ctx, hwm)
				if err != nil {
					log.WithError(err).Warn("LLM metrics: scan failed")
					return
				}
				pt := pricing.Load()
				for _, row := range rows {
					m.record(ctx, row, pt)
				}
				hwm = newHWM
			})
		}
	}
}
