package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Explicit opt-in reads an existing store without opening the statev2 writer,
// applying migrations, starting services, or modifying retained data.
func TestHistoryAnalyticsReadOnlyLatency(t *testing.T) {
	path := os.Getenv("OCMAN_HISTORY_LATENCY_DB")
	if path == "" {
		t.Skip("set OCMAN_HISTORY_LATENCY_DB to measure an existing history store read-only")
	}
	ids := strings.Split(os.Getenv("OCMAN_HISTORY_LATENCY_INSTANCES"), ",")
	if len(ids) == 1 && ids[0] == "" {
		t.Fatal("set OCMAN_HISTORY_LATENCY_INSTANCES to the nonempty source instance IDs")
	}
	reader, err := OpenHistoryAnalytics(path, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	since := time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
	month := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	for _, query := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"scope", reader.checkHistoryScope},
		{"scope-unchanged", reader.checkHistoryScope},
		{"catalog-recents", func(ctx context.Context) error { _, err := reader.GetRecentModels(ctx, 50, 10); return err }},
		{"catalog-defaults", func(ctx context.Context) error { _, err := reader.GetSessionDefaults(ctx, "", ""); return err }},
		{"models-7d", func(ctx context.Context) error { _, err := reader.GetModelUsage(ctx, since, ""); return err }},
		{"activity-7d", func(ctx context.Context) error { _, err := reader.GetDailyActivity(ctx, since, "", ""); return err }},
		{"metrics-7d", func(ctx context.Context) error {
			_, err := reader.GetMetricsDashboard(ctx, MetricsDashboardOptions{Since: since, Days: 7})
			return err
		}},
		{"stats-all", func(ctx context.Context) error { _, err := reader.GetStats(ctx); return err }},
		{"models-30d", func(ctx context.Context) error { _, err := reader.GetModelUsage(ctx, month, ""); return err }},
		{"activity-365d", func(ctx context.Context) error { _, err := reader.GetDailyActivity(ctx, 0, "", ""); return err }},
		{"concurrency-30d", func(ctx context.Context) error {
			_, err := reader.GetSessionConcurrency(ctx, month, time.Now().UnixMilli(), "")
			return err
		}},
		{"runtime-30d", func(ctx context.Context) error {
			_, err := reader.GetAgentRunHours(ctx, month, time.Now().UnixMilli(), "", nil)
			return err
		}},
		{"metrics-30d", func(ctx context.Context) error {
			_, err := reader.GetMetricsDashboard(ctx, MetricsDashboardOptions{Since: month, Days: 30})
			return err
		}},
		{"performance-30d", func(ctx context.Context) error {
			_, err := reader.GetMetricsPerformance(ctx, MetricsDashboardOptions{Since: month, Days: 30})
			return err
		}},
		{"request-log-30d", func(ctx context.Context) error {
			_, err := reader.GetMetricsLog(ctx, MetricsDashboardOptions{Since: month, Days: 30, RequestLimit: 20}, MetricsLogRequests)
			return err
		}},
	} {
		t.Run(query.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start := time.Now()
			err := query.run(ctx)
			t.Logf("%s: %s", query.name, time.Since(start))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryJoinedMessagesCheckSessionOnlyOnce(t *testing.T) {
	path, _ := historyPerformanceStore(t)
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	rows, err := reader.db.Query(`EXPLAIN QUERY PLAN SELECT count(*) FROM ` + reader.messagesFrom(0, true) + ` WHERE json_extract(m.data,'$.role')='user'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	lookups := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "sqlite_autoindex_session_1") {
			lookups++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if lookups > 1 {
		t.Fatalf("joined analytics checks session %d times per message", lookups)
	}
}

func historyPerformanceStore(t *testing.T) (string, *statev2.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := statev2.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "one", OwnerID: "owner", Platform: "p", SourceKey: "local"}); err != nil {
		t.Fatal(err)
	}
	addHistoryPerformanceSession(t, store, "first")
	return path, store
}

func addHistoryPerformanceSession(t *testing.T, store *statev2.DB, id string) {
	t.Helper()
	if _, err := store.Commit(t.Context(), statev2.Batch{
		Session:  statev2.Session{Key: statev2.Key{InstanceID: "one", SessionID: id}},
		Messages: []statev2.Message{{ID: "message-" + id, Role: "assistant", CreatedAt: 1}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryScopeDetectsLaterCollisionsAndPurgeRepair(t *testing.T) {
	for _, collision := range []string{"session", "message"} {
		t.Run(collision, func(t *testing.T) {
			path, store := historyPerformanceStore(t)
			if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "two", OwnerID: "owner", Platform: "p", SourceKey: "other"}); err != nil {
				t.Fatal(err)
			}
			reader, err := OpenHistoryAnalytics(path, []string{"one", "two"})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if err := reader.checkHistoryScope(t.Context()); err != nil {
				t.Fatal(err)
			}
			sessionID, messageID := "other-session", "other-message"
			if collision == "session" {
				sessionID = "first"
			} else {
				messageID = "message-first"
			}
			key := statev2.Key{InstanceID: "two", SessionID: sessionID}
			if _, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: key}, Messages: []statev2.Message{{ID: messageID, Role: "user"}}}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := reader.checkHistoryScope(t.Context()); err == nil || !strings.Contains(err.Error(), collision+" IDs collide") {
					t.Fatalf("missed later %s collision: %v", collision, err)
				}
			}
			if _, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: key, SourceDeleted: true}, ExpectedRevision: 1}); err != nil {
				t.Fatal(err)
			}
			if err := reader.checkHistoryScope(t.Context()); err != nil {
				t.Fatalf("hidden source blocked active scope: %v", err)
			}
			if _, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: key}, ExpectedRevision: 2}); err != nil {
				t.Fatal(err)
			}
			if err := reader.checkHistoryScope(t.Context()); err == nil {
				t.Fatal("restored collision was not detected")
			}
			if err := store.Purge(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			if err := reader.checkHistoryScope(t.Context()); err != nil {
				t.Fatalf("purge did not repair scope: %v", err)
			}
		})
	}
}

func TestHistoryScopeReusesValidationUntilHistoryChanges(t *testing.T) {
	path, store := historyPerformanceStore(t)
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.db.SetMaxOpenConns(1)
	if err := reader.checkHistoryScope(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Make any repeated full scan observable without wall-clock assertions.
	if _, err := reader.db.Exec(`DROP VIEW history_fact; CREATE TEMP VIEW history_fact AS SELECT json('invalid') AS id`); err != nil {
		t.Fatal(err)
	}
	if err := reader.checkHistoryScope(t.Context()); err != nil {
		t.Fatalf("rescanned unchanged history: %v", err)
	}
	if _, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "one", SessionID: "two"}}, Messages: []statev2.Message{{ID: "message-first", Role: "assistant"}}}); err != nil {
		t.Fatal(err)
	}
	if err := reader.checkHistoryScope(t.Context()); err == nil {
		t.Fatal("did not revalidate changed history")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reader.checkHistoryScope(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestHistoryScopeRejectsLegacyReader(t *testing.T) {
	reader := openTestDB(t)
	defer reader.Close()
	if err := reader.checkHistoryScope(t.Context()); err == nil {
		t.Fatal("accepted a non-history reader")
	}
}

func TestHistoryRecentWindowDoesNotScanMessagesForEverySession(t *testing.T) {
	path, _ := historyPerformanceStore(t)
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, join := range []bool{false, true} {
		t.Run(fmt.Sprint(join), func(t *testing.T) {
			rows, err := reader.db.Query(`EXPLAIN QUERY PLAN SELECT m.id FROM `+reader.messagesFrom(time.Now().UnixMilli(), join)+` WHERE json_extract(m.data,'$.role')='assistant' AND m.time_created>=?`, time.Now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var first string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if first == "" {
					first = detail
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(first, "USING INDEX analytics_message_role") {
				t.Fatalf("recent window repeats message scan for each session: %s", first)
			}
		})
	}
}
