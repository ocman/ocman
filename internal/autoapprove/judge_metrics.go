package autoapprove

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type judgeScores struct {
	Choice                             string
	Safe, Unsafe, Uncertain, Threshold float64
}

// Record one evaluation after cache gates, including failures and unavailable
// backends. Never put permission data or owner/session identifiers in labels.
func recordJudgeMetrics(ctx context.Context, backend string, elapsed time.Duration, result JudgeResult) {
	outcome := "needs_user"
	if result.EvaluationFailed {
		outcome = "error"
	} else if result.Verdict == verdictSafe {
		outcome = "approved"
	} else if result.Verdict == verdictUnsafe {
		outcome = "not_approved"
	}
	opts := metric.WithAttributes(attribute.String("backend", backend), attribute.String("outcome", outcome))
	meter := telemetry.Meter()
	calls, err := meter.Int64Counter("ocman.judge.calls", metric.WithDescription("Permission judge evaluations after cache gates."), metric.WithUnit("{call}"))
	if err != nil {
		otel.Handle(err)
	} else {
		calls.Add(ctx, 1, opts)
	}
	duration, err := meter.Float64Histogram("ocman.judge.duration", metric.WithUnit("s"), metric.WithDescription("Permission judge evaluation duration."), metric.WithExplicitBucketBoundaries(.1, .5, 1, 2, 5, 10, 30, 60, 120))
	if err != nil {
		otel.Handle(err)
	} else {
		duration.Record(ctx, elapsed.Seconds(), opts)
	}
	if result.scores != nil {
		recordJudgeScores(ctx, backend, outcome, *result.scores)
	}
}

func recordJudgeScores(ctx context.Context, backend, outcome string, scores judgeScores) {
	attrs := []attribute.KeyValue{attribute.String("backend", backend), attribute.String("outcome", outcome), attribute.String("choice", scores.Choice)}
	meter := telemetry.Meter()
	bounds := metric.WithExplicitBucketBoundaries(0, .1, .2, .3, .4, .5, .6, .7, .8, .9, .95, .99, 1)
	probability, err := meter.Float64Histogram("ocman.judge.probability", metric.WithUnit("1"), metric.WithDescription("Validated TypeSafe probabilities for each candidate verdict."), bounds)
	if err != nil {
		otel.Handle(err)
	} else {
		for _, score := range []struct {
			answer string
			value  float64
		}{{"safe", scores.Safe}, {"unsafe", scores.Unsafe}, {"uncertain", scores.Uncertain}} {
			probability.Record(ctx, score.value, metric.WithAttributes(append(attrs, attribute.String("answer", score.answer))...))
		}
	}
	threshold, err := meter.Float64Histogram("ocman.judge.safe_threshold", metric.WithUnit("1"), metric.WithDescription("Configured safe probability threshold for validated TypeSafe evaluations."), bounds)
	if err != nil {
		otel.Handle(err)
	} else {
		threshold.Record(ctx, scores.Threshold, metric.WithAttributes(attrs...))
	}
}
