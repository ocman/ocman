package db

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

func TestStoredScannerIgnoresHiddenSessionsAndKeepsMetricLabels(t *testing.T) {
	path, store := historyPerformanceStore(t)
	for _, row := range []struct {
		id     string
		at     int64
		hidden bool
	}{{"visible", 20, false}, {"hidden", 100, true}} {
		if _, err := store.Commit(t.Context(), statev2.Batch{
			Session: statev2.Session{Key: statev2.Key{InstanceID: "one", SessionID: row.id}, SourceDeleted: row.hidden},
			Messages: []statev2.Message{{ID: row.id, Role: "assistant", CreatedAt: row.at,
				Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":" model ","finish":" stop ","time":{"created":10,"completed":15}}`)}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got, err := reader.GetMaxMessageTime(t.Context()); err != nil || got != 20 {
		t.Fatalf("scanner high-water mark included hidden history: %d %v", got, err)
	}
	rows, next, err := reader.GetNewAssistantMessages(t.Context(), 1)
	if err != nil || next != 20 || len(rows) != 1 {
		t.Fatalf("scanner rows: %+v next=%d %v", rows, next, err)
	}
	if rows[0].Model != "p/ model " || rows[0].StopReason != "stop" || rows[0].DurationMs != 5 {
		t.Fatalf("metric labels or timing changed: %+v", rows[0])
	}
}
