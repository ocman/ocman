package telemetry

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestConversationWorkUsesBoundedLabelsAndRecordsValues(t *testing.T) {
	reader := withTestMeterProvider(t)
	old := conversationWork
	conversationWork = newConversationMetrics()
	defer func() { conversationWork = old }()
	ConversationWork(t.Context(), ConversationDisplay)(nil, 64)
	ConversationWork(t.Context(), "private-session-id")(errors.New("secret"), -1)
	metrics := collectMetrics(t, reader)
	work := metrics["ocman.conversation.work.items"].Data.(metricdata.Histogram[int64])
	duration := metrics["ocman.conversation.work.duration"].Data.(metricdata.Histogram[float64])
	if len(work.DataPoints) != 2 || len(duration.DataPoints) != 2 {
		t.Fatal("missing phase observations")
	}
	for _, p := range work.DataPoints {
		phase, _ := p.Attributes.Value("phase")
		outcome, _ := p.Attributes.Value("outcome")
		if p.Attributes.Len() != 2 || p.Count != 1 {
			t.Fatalf("unbounded attributes or wrong count: %+v", p)
		}
		switch phase.AsString() {
		case "display":
			if p.Sum != 64 || outcome.AsString() != "ok" {
				t.Fatalf("display=%+v", p)
			}
		case "other":
			if p.Sum != 0 || outcome.AsString() != "error" {
				t.Fatalf("error=%+v", p)
			}
		default:
			t.Fatalf("private label: %+v", p)
		}
	}
	for _, p := range duration.DataPoints {
		if p.Count != 1 || p.Sum <= 0 {
			t.Fatalf("duration=%+v", p)
		}
	}
}
