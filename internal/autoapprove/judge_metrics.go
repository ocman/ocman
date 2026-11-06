package autoapprove

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

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
	if len(result.ruleScores) > 0 {
		recordJudgeScores(ctx, backend, outcome, result.ruleScores)
	}
}

func recordJudgeScores(ctx context.Context, backend, outcome string, scores []ruleScore) {
	attrs := []attribute.KeyValue{attribute.String("backend", backend), attribute.String("outcome", outcome)}
	meter := telemetry.Meter()
	bounds := metric.WithExplicitBucketBoundaries(0, .1, .2, .3, .4, .5, .6, .7, .8, .9, .95, .99, 1)
	probability, err := meter.Float64Histogram("ocman.judge.probability", metric.WithUnit("1"), metric.WithDescription("Validated TypeSafe match probabilities for each rule."), bounds)
	if err != nil {
		otel.Handle(err)
	} else {
		for _, score := range scores {
			probability.Record(ctx, score.Probability, metric.WithAttributes(append(attrs, attribute.String("rule", score.ID), attribute.String("tier", string(score.Tier)))...))
		}
	}
	threshold, err := meter.Float64Histogram("ocman.judge.rule_threshold", metric.WithUnit("1"), metric.WithDescription("Effective probability threshold for each validated TypeSafe rule."), bounds)
	if err != nil {
		otel.Handle(err)
	} else {
		for _, score := range scores {
			threshold.Record(ctx, score.Threshold, metric.WithAttributes(append(attrs, attribute.String("rule", score.ID), attribute.String("tier", string(score.Tier)))...))
		}
	}
}
