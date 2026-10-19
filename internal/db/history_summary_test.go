package db

import (
	"context"
	"testing"
	"time"
)

func TestHistoryAnalyticsNeverReadsPayloadProjections(t *testing.T) {
	path, _ := historyPerformanceStore(t)
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.db.SetMaxOpenConns(1)
	if _, err := reader.db.Exec(`DROP VIEW message; DROP VIEW part;
		CREATE TEMP VIEW message AS SELECT json('invalid') AS data;
		CREATE TEMP VIEW part AS SELECT json('invalid') AS data`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"scope", reader.ValidateHistoryScope},
		{"stats", func(ctx context.Context) error { _, err := reader.GetStats(ctx); return err }},
		{"activity", func(ctx context.Context) error { _, err := reader.GetDailyActivity(ctx, 0, "", ""); return err }},
		{"models", func(ctx context.Context) error { _, err := reader.GetModelUsage(ctx, 0, ""); return err }},
		{"hourly", func(ctx context.Context) error { _, err := reader.GetHourlyActivity(ctx, 0, ""); return err }},
		{"tokens", func(ctx context.Context) error {
			_, err := reader.GetHourlyTokensByModel(ctx, 0, 0, "", "")
			return err
		}},
		{"metrics", func(ctx context.Context) error {
			_, err := reader.GetMetricsDashboard(ctx, MetricsDashboardOptions{})
			return err
		}},
		{"performance", func(ctx context.Context) error {
			_, err := reader.GetMetricsPerformance(ctx, MetricsDashboardOptions{})
			return err
		}},
		{"logs", func(ctx context.Context) error {
			_, err := reader.GetMetricsLog(ctx, MetricsDashboardOptions{}, MetricsLogRequests)
			return err
		}},
		{"concurrency", func(ctx context.Context) error {
			_, err := reader.GetSessionConcurrency(ctx, 0, time.Now().UnixMilli(), "")
			return err
		}},
		{"runtime", func(ctx context.Context) error {
			_, err := reader.GetAgentRunHours(ctx, 0, time.Now().UnixMilli(), "", nil)
			return err
		}},
	} {
		t.Run(query.name, func(t *testing.T) {
			if err := query.run(t.Context()); err != nil {
				t.Fatal("read payload projection", err)
			}
		})
	}
}
