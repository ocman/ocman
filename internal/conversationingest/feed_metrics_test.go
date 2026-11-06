package conversationingest

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestSuccessfulFeedDrainRecordsAdmittedItems(t *testing.T) {
	// Package-level OTel instruments bind to the first installed provider.
	// Isolate that binding so repeated runs and other metric tests stay independent.
	if os.Getenv("OCMAN_TEST_DRAIN_METRICS_CHILD") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSuccessfulFeedDrainRecordsAdmittedItems$", "-test.count=1", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "OCMAN_TEST_DRAIN_METRICS_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated metric assertion failed: %v\n%s", err, output)
		}
		return
	}
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context()) // initial catch-up has no queued items
	f.snapshots["session"] = historySnapshot("updated by the feed")
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"message"}})
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "updated by the feed" || w.lastFailure != "" {
		t.Fatalf("drain did not successfully apply its item: text=%q failure=%q", text, w.lastFailure)
	}
	var result metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &result); err != nil {
		t.Fatal(err)
	}
	for _, scope := range result.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "ocman.conversation.work.items" {
				continue
			}
			for _, point := range m.Data.(metricdata.Histogram[int64]).DataPoints {
				phase, _ := point.Attributes.Value("phase")
				outcome, _ := point.Attributes.Value("outcome")
				if phase.AsString() == "drain" && outcome.AsString() == "ok" {
					if point.Count != 2 || point.Sum != 1 {
						t.Fatalf("successful drains recorded count=%d items=%d, want count=2 items=1", point.Count, point.Sum)
					}
					return
				}
			}
		}
	}
	t.Fatal("successful drain metric was not recorded")
}
