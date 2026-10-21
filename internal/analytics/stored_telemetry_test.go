package analytics

import (
	"testing"
)

func TestTelemetryStatsReadStatev2AfterNativeDatabaseCloses(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	reader, release, reason := h.projection(t.Context())
	if reader == nil {
		t.Fatal(reason)
	}
	want, err := reader.GetStats(t.Context())
	release()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.legacy.Close(); err != nil {
		t.Fatal(err)
	}
	f.refreshStats(t.Context())
	got := f.cachedStats()
	if got == nil || got.TotalSessions != want.TotalSessions || got.TotalTokensIn != want.TotalTokensIn {
		t.Fatalf("telemetry still read native DB: got=%+v want=%+v", got, want)
	}
	// An incomplete import never falls back to a native reader or clears the
	// last successfully measured statev2 snapshot.
	h.Instances = func() ([]string, bool) { return nil, false }
	f.refreshStats(t.Context())
	if f.cachedStats() != got {
		t.Fatal("unavailable import replaced last good metrics")
	}
}

func TestStoredLLMMetricsRetriesInitialImportAndKeepsCursorOnFailure(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	reader, release, reason := h.projection(t.Context())
	if reader == nil {
		t.Fatal(reason)
	}
	want, err := reader.GetMaxMessageTime(t.Context())
	release()
	if err != nil || want == 0 {
		t.Fatalf("max time: %d %v", want, err)
	}
	_ = f.legacy.Close()
	instances := h.Instances
	h.Instances = func() ([]string, bool) { return nil, false }
	hwm, seeded := int64(0), false
	f.runStoredLLMMetricsTick(t.Context(), nil, &hwm, &seeded)
	if seeded || hwm != 0 {
		t.Fatal("unavailable initial import advanced cursor")
	}
	h.Instances = instances
	f.runStoredLLMMetricsTick(t.Context(), nil, &hwm, &seeded)
	if !seeded || hwm != want {
		t.Fatalf("recovered import did not seed cursor: %v %d want %d", seeded, hwm, want)
	}
	hwm = 0
	f.runStoredLLMMetricsTick(t.Context(), nil, &hwm, &seeded)
	if hwm != want {
		t.Fatalf("statev2 scan failed: %d want %d", hwm, want)
	}
	h.Instances = func() ([]string, bool) { return nil, false }
	f.runStoredLLMMetricsTick(t.Context(), nil, &hwm, &seeded)
	if hwm != want {
		t.Fatal("unavailable import consumed telemetry cursor")
	}
	f.deps.HistoryAnalytics = nil
	if err := f.withStoredAnalytics(t.Context(), nil); err == nil {
		t.Fatal("missing history accepted")
	}
}
