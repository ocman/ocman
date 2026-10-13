package db

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestAnalyticsSourceRowsJoinTheMirror(t *testing.T) {
	d, _ := openMirrored(t)
	now := time.Now().UnixMilli()
	seedMirrorSource(t, d, now)
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	var sinces []int64
	d.AddAnalyticsSource(func(_ context.Context, since, _ int64) ([]AnalyticsSession, []AnalyticsMessage, error) {
		sinces = append(sinces, since)
		return []AnalyticsSession{{ID: "c1", Directory: "/p/claude", Title: "Claude", TimeCreated: now - 1000}},
			[]AnalyticsMessage{
				{ID: "cu", SessionID: "c1", TimeCreated: now - 900, Settled: true, Data: `{"role":"user"}`},
				{ID: "ca", SessionID: "c1", TimeCreated: now - 800, Settled: true, Data: `{"role":"assistant","providerID":"anthropic","modelID":"claude-x","cost":0,"finish":"stop","tokens":{"input":1000,"output":200,"cache":{"read":30,"write":4}},"time":{"created":1,"completed":2}}`},
				// Belongs to no session: the orphan cleanup drops it.
				{ID: "orphan", SessionID: "gone", TimeCreated: now - 700, Settled: true, Data: `{"role":"assistant"}`},
			}, nil
	})
	if !d.mirror.fullDue() {
		t.Fatal("adding a source must schedule a full rebuild")
	}
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sinces) != 1 || sinces[0] != 0 {
		t.Fatalf("source since = %v, want one full read", sinces)
	}
	dash, err := d.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{Dir: "/p/claude"})
	if err != nil {
		t.Fatal(err)
	}
	s := dash.Summary
	if dash.TotalRequests != 1 || s.InputTokens != 1000 || s.OutputTokens != 200 || s.CacheReadTokens != 30 || s.CacheWriteTokens != 4 {
		t.Fatalf("claude summary = %+v", s)
	}
	if ids := mirrorIDs(t, d); slices.Contains(ids, "orphan") || !slices.Contains(ids, "ca") || !slices.Contains(ids, "a-old") {
		t.Fatalf("mirror ids = %v", ids)
	}

	// An incremental sync re-reads only the window and keeps the session.
	d.mirror.lastSync.Store(0)
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sinces) != 2 || sinces[1] == 0 {
		t.Fatalf("incremental since = %v", sinces)
	}
	if ids := mirrorIDs(t, d); !slices.Contains(ids, "ca") {
		t.Fatalf("incremental sync lost claude rows: %v", ids)
	}
}

func TestAnalyticsSourceErrorFailsTheSync(t *testing.T) {
	d, _ := openMirrored(t)
	d.AddAnalyticsSource(func(context.Context, int64, int64) ([]AnalyticsSession, []AnalyticsMessage, error) {
		return nil, nil, errors.New("boom")
	})
	if err := d.SyncAnalyticsMirror(t.Context()); err == nil {
		t.Fatal("source error was swallowed")
	}
	none := &DB{}
	none.AddAnalyticsSource(nil) // no mirror: ignored
	if err := none.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
}
