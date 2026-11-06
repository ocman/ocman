package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type ConversationPhase string

const (
	ConversationSettle  ConversationPhase = "settle"
	ConversationDisplay ConversationPhase = "display"
	ConversationWatch   ConversationPhase = "watch"
	ConversationDrain   ConversationPhase = "drain"
	ConversationCommit  ConversationPhase = "commit"
)

type conversationMetrics struct {
	duration metric.Float64Histogram
	work     metric.Int64Histogram
}

func newConversationMetrics() conversationMetrics {
	m := Meter()
	duration, _ := m.Float64Histogram("ocman.conversation.work.duration", metric.WithUnit("s"))
	work, _ := m.Int64Histogram("ocman.conversation.work.items", metric.WithUnit("{item}"))
	return conversationMetrics{duration, work}
}

var conversationWork = newConversationMetrics()

// ConversationWork records only fixed phase/outcome labels, never session IDs,
// directories, errors or source addresses. Items count admitted work, not bytes.
func ConversationWork(ctx context.Context, phase ConversationPhase) func(error, int64) {
	start := time.Now()
	// Keep accidental callers from introducing unbounded phase labels.
	switch phase {
	case ConversationSettle, ConversationDisplay, ConversationWatch, ConversationDrain, ConversationCommit:
	default:
		phase = "other"
	}
	return func(err error, items int64) {
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}
		attrs := metric.WithAttributes(attribute.String("phase", string(phase)), attribute.String("outcome", outcome))
		conversationWork.duration.Record(ctx, time.Since(start).Seconds(), attrs)
		conversationWork.work.Record(ctx, max(0, items), attrs)
	}
}
