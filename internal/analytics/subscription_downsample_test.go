package analytics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSubscriptionHistoryDownsamplesOldHoursWithoutAveraging(t *testing.T) {
	m := historyModule(t)
	now := time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC)
	oldHour := now.Add(-40 * 24 * time.Hour).Truncate(time.Hour)
	recentHour := now.Add(-2 * time.Hour).Truncate(time.Hour)
	seed := func(id string, at time.Time, value float64, reset string) {
		t.Helper()
		p := subscriptionProviderUsage{ID: id, Name: id, Status: "ok", Windows: []subscriptionUsageWindow{{Name: "5 hours", UsedPercent: value, ResetsAt: reset}}}
		payload, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.deps.Store.SQL().Exec(`INSERT INTO subscription_usage_samples VALUES (?, ?, ?)`, id, at.UnixMilli(), string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 12 {
		at := oldHour.Add(time.Duration(index) * subscriptionSampleInterval)
		reset, value := "before-reset", float64(80+index)
		if index >= 6 {
			reset, value = "after-reset", float64(index)
		}
		seed("codex", at, value, reset)
		seed("claudecode", at, float64(index*2), "weekly")
		seed("codex", recentHour.Add(time.Duration(index)*subscriptionSampleInterval), float64(index), "recent")
	}
	seed("codex", now.Add(-400*24*time.Hour), 50, "older-than-a-year")
	for range 2 { // compaction must be idempotent
		if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, nil); err != nil {
			t.Fatal(err)
		}
	}
	history, err := readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), now, 500)
	if err != nil || len(history.Samples) != 15 {
		t.Fatalf("got %d samples, want 12 recent and 3 hourly; err=%v", len(history.Samples), err)
	}
	var found bool
	for _, sample := range history.Samples {
		interval := subscriptionSampleInterval.Milliseconds()
		if sample.SampledAt < now.Add(-subscriptionHistoryFineRetention).Truncate(time.Hour).UnixMilli() {
			interval = time.Hour.Milliseconds()
		}
		if sample.SampleIntervalMs != interval {
			t.Fatalf("wrong resolution: %+v", sample)
		}
		if sample.Provider.ID == "codex" && sample.SampledAt == oldHour.Add(55*time.Minute).UnixMilli() {
			found = true
			if sample.Provider.Windows[0].UsedPercent != 11 || sample.Provider.Windows[0].ResetsAt != "after-reset" {
				t.Fatalf("hourly reading was averaged or reset metadata changed: %+v", sample)
			}
		}
	}
	if !found {
		t.Fatal("latest actual hourly snapshot was discarded")
	}
}

func TestSubscriptionDownsamplingWaitsForCompleteOldHour(t *testing.T) {
	m := historyModule(t)
	now := time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC)
	hour := now.Add(-subscriptionHistoryFineRetention).Truncate(time.Hour)
	for _, offset := range []time.Duration{0, 25 * time.Minute, 40 * time.Minute} {
		if _, err := m.deps.Store.SQL().Exec(`INSERT INTO subscription_usage_samples VALUES ('codex', ?, '{"id":"codex","status":"ok","windows":[]}')`, hour.Add(offset).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, nil); err != nil {
		t.Fatal(err)
	}
	history, err := readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), now, 0)
	if err != nil || len(history.Samples) != 3 {
		t.Fatalf("partially old hour compacted early: %+v %v", history, err)
	}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	history, err = readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), now.Add(time.Hour), 0)
	if err != nil || len(history.Samples) != 1 || history.Samples[0].SampledAt != hour.Add(40*time.Minute).UnixMilli() || history.Samples[0].SampleIntervalMs != time.Hour.Milliseconds() {
		t.Fatalf("hour did not compact to its latest snapshot: %+v %v", history, err)
	}
}
