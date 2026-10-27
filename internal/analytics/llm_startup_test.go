package analytics

import (
	"context"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

func TestStoredLLMMetricsStartupLogging(t *testing.T) {
	for _, tc := range []struct {
		name        string
		warming     bool
		canceled    bool
		wantLevel   log.Level
		wantEntries int
	}{
		{name: "initializing", warming: true, wantLevel: log.DebugLevel, wantEntries: 1},
		{name: "unavailable", wantLevel: log.WarnLevel, wantEntries: 1},
		{name: "shutdown", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := log.StandardLogger()
			oldLevel := logger.GetLevel()
			oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
			hook := test.NewGlobal()
			logger.SetLevel(log.DebugLevel)
			t.Cleanup(func() {
				logger.SetLevel(oldLevel)
				logger.ReplaceHooks(oldHooks)
			})
			f := newFixture(t)
			if tc.warming {
				f.seedHistory.Instances = func() ([]string, bool) { return nil, false }
			} else {
				f.deps.HistoryAnalytics = func() *HistoryAnalytics { return nil }
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			hwm, seeded := int64(42), false
			hook.Reset()
			f.runStoredLLMMetricsTick(ctx, nil, &hwm, &seeded)
			if hwm != 42 || seeded {
				t.Fatalf("failed initialization changed scanner state: hwm=%d seeded=%v", hwm, seeded)
			}
			entries := hook.AllEntries()
			if len(entries) != tc.wantEntries {
				t.Fatalf("got %d log entries, want %d", len(entries), tc.wantEntries)
			}
			if len(entries) > 0 && entries[0].Level != tc.wantLevel {
				t.Fatalf("log level = %v, want %v", entries[0].Level, tc.wantLevel)
			}
			if tc.warming {
				f.seedHistory.Instances = func() ([]string, bool) { return []string{"fixture"}, true }
				f.runStoredLLMMetricsTick(ctx, nil, &hwm, &seeded)
				if !seeded || hwm != 0 {
					t.Fatalf("retry did not seed empty history: hwm=%d seeded=%v", hwm, seeded)
				}
				f.runStoredLLMMetricsTick(ctx, nil, &hwm, &seeded)
			}
		})
	}
}
