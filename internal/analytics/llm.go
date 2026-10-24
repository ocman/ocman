package analytics

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
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
	for _, c := range []struct {
		name, desc, unit string
		dst              *metric.Int64Counter
	}{
		{"ocman.llm.requests", "LLM assistant turns processed.", "{request}", &m.requests},
		{"ocman.llm.tokens.input", "Input tokens consumed by LLM turns.", "{token}", &m.tokensInput},
		{"ocman.llm.tokens.output", "Output tokens produced by LLM turns.", "{token}", &m.tokensOutput},
		{"ocman.llm.tokens.cache_read", "Cache-read tokens across LLM turns.", "{token}", &m.cacheRead},
		{"ocman.llm.tokens.cache_write", "Cache-write tokens across LLM turns.", "{token}", &m.cacheWrite},
	} {
		var err error
		if *c.dst, err = meter.Int64Counter(c.name, metric.WithDescription(c.desc), metric.WithUnit(c.unit)); err != nil {
			return nil, err
		}
	}
	for _, c := range []struct {
		name, desc string
		dst        *metric.Float64Counter
	}{
		{"ocman.llm.cost", "Platform-reported API cost of LLM turns.", &m.cost},
		{"ocman.llm.calc_cost", "Estimated API cost calculated from token counts and public pricing.", &m.calcCost},
	} {
		var err error
		if *c.dst, err = meter.Float64Counter(c.name, metric.WithDescription(c.desc), metric.WithUnit("{USD}")); err != nil {
			return nil, err
		}
	}
	var err error
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
func (m *Module) runLLMMetricsLoop(ctx context.Context) {
	if !telemetry.Enabled() || m.deps.HistoryAnalytics == nil {
		return
	}

	metrics, err := newLLMMetrics(telemetry.Meter())
	if err != nil {
		log.WithError(err).Warn("failed to create LLM metrics instruments")
		return
	}

	// Initialise high-water mark to the current max so we don't
	// replay the entire history on startup.
	var hwm int64
	seeded := false
	module.Every(ctx, "llm-metrics", llmMetricsScanInterval, func(ctx context.Context) {
		m.runStoredLLMMetricsTick(ctx, metrics, &hwm, &seeded)
	})
}

func (m *Module) runStoredLLMMetricsTick(ctx context.Context, metrics *llmMetrics, hwm *int64, seeded *bool) {
	if *seeded {
		m.runLLMMetricsTick(ctx, metrics, hwm)
		return
	}
	var next int64
	err := m.withStoredAnalytics(ctx, func(reader *db.DB) error { var err error; next, err = reader.GetMaxMessageTime(ctx); return err })
	if err != nil {
		if ctx.Err() == nil {
			log.WithError(err).Warn("LLM metrics: initial history snapshot unavailable; will retry")
		}
		return
	}
	*hwm = next
	*seeded = true
}

func (m *Module) runLLMMetricsTick(ctx context.Context, metrics *llmMetrics, hwm *int64) {
	// Telemetry must keep collecting when no ocman metrics page is visible;
	// external dashboards do not hold browser activity leases.
	getMessages := m.getNewAssistantMessages
	if getMessages == nil {
		getMessages = func(ctx context.Context, since int64) (rows []db.LLMMessageRow, next int64, err error) {
			err = m.withStoredAnalytics(ctx, func(reader *db.DB) error { rows, next, err = reader.GetNewAssistantMessages(ctx, since); return err })
			return rows, next, err
		}
	}
	rows, newHWM, err := getMessages(ctx, *hwm)
	if err != nil {
		log.WithError(err).Warn("LLM metrics: scan failed")
		return
	}
	if metrics != nil {
		pt := pricing.Load()
		for _, row := range rows {
			metrics.record(ctx, row, pt)
		}
	}
	*hwm = newHWM
}
