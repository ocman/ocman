package db

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

func TestHistoryMetricSessionIdentityPreservesNativeMessagesAndSnapshotMetadata(t *testing.T) {
	path, store := historyPerformanceStore(t)
	if _, err := store.Commit(t.Context(), statev2.Batch{ExpectedRevision: 1, Session: statev2.Session{Key: statev2.Key{InstanceID: "one", SessionID: "first"}, Title: "retained title", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	opts := MetricsDashboardOptions{SessionIdentity: func(ctx context.Context, instance, native string) (string, error) {
		if instance != "one" || native != "first" {
			t.Fatalf("source identity lost: %s %s", instance, native)
		}
		return "ocm_history", nil
	}}
	metrics, err := reader.GetMetricsDashboard(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.Requests) != 1 || metrics.Requests[0].SessionID != "ocm_history" || metrics.Requests[0].ID != "message-first" {
		t.Fatalf("request IDs: %+v", metrics.Requests)
	}
	if len(metrics.Sessions) != 1 || metrics.Sessions[0].ID != "ocm_history" || metrics.Sessions[0].Title != "retained title" || metrics.Sessions[0].Directory != "/repo" {
		t.Fatalf("session metadata lost: %+v", metrics.Sessions)
	}
	if len(metrics.Projects) != 1 || metrics.Projects[0].Directory != "/repo" {
		t.Fatalf("project metadata lost: %+v", metrics.Projects)
	}
	opts.SessionIdentity = func(context.Context, string, string) (string, error) { return "", errors.New("identity unavailable") }
	if _, err := reader.GetMetricsLog(t.Context(), opts, MetricsLogRequests); err == nil {
		t.Fatal("failed identity lookup leaked a native session ID")
	}
}
