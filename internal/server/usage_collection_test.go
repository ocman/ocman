package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/analytics"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/statev2/statev2test"
	"github.com/ocman/ocman/internal/usagecollection"
	"github.com/ocman/ocman/internal/usagewire"
)

func TestDailyUsageUsesInstanceOptOut(t *testing.T) {
	t.Setenv("OCMAN_USAGE_DAILY_MODELS", "")
	ctx := t.Context()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	history := statev2test.Open(t)
	if err := history.RegisterInstance(ctx, statev2.Instance{ID: "local", OwnerID: "owner", Platform: "opencode", SourceKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
	at := day.Add(time.Hour).UnixMilli()
	if _, err := history.Commit(ctx, statev2.Batch{
		Session: statev2.Session{Key: statev2.Key{InstanceID: "local", SessionID: "one"}, CreatedAt: at, UpdatedAt: at},
		Messages: []statev2.Message{{ID: "message", Role: "assistant", CreatedAt: at,
			Extensions: json.RawMessage(`{"role":"assistant","modelID":"gpt-5.4","tokens":{"input":7,"output":3}}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "opted-out"} {
		t.Run(name, func(t *testing.T) {
			db, err := state.Open(statetest.Path(t), usagecollection.Schema)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.SQL().ExecContext(ctx, `INSERT INTO usage_collection(id,installation_id,daily_from) VALUES(1,?,?)`, "3f70f326-5593-4b78-a31f-c67a23920334", day.Format(time.DateOnly)); err != nil {
				t.Fatal(err)
			}
			if name == "opted-out" {
				if err := db.SetSetting(ctx, usagecollection.EnabledSetting, "false"); err != nil {
					t.Fatal(err)
				}
			}
			reports := make(chan usagewire.Report, 1)
			workerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer cancel()
				var report usagewire.Report
				if r.Method != http.MethodPost || r.URL.Path != "/v1/usage/daily" || json.NewDecoder(r.Body).Decode(&report) != nil {
					t.Errorf("unexpected usage request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				reports <- report
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(relay.Close)
			s := Server{stateDB: db, relayURL: relay.URL, version: "0.276.0", historyAnalytics: &analytics.HistoryAnalytics{
				Store: history, Instances: func() ([]string, bool) { return []string{"local"}, true },
			}}
			s.usageCollectionModule().Run(workerCtx)
			select {
			case got := <-reports:
				want := usagewire.ModelUsage{Model: "gpt-5.4", Sessions: 1, InputTokens: 7, OutputTokens: 3}
				if name != "default" || got.Date != day.Format(time.DateOnly) || got.Daily == nil || got.Daily.Sessions != 1 || len(got.Daily.Models) != 1 || got.Daily.Models[0] != want {
					t.Fatalf("unexpected daily report: %+v (%+v)", got, got.Daily)
				}
			default:
				if name == "default" {
					t.Fatal("no daily report with default usage settings")
				}
				var from string
				if err := db.SQL().QueryRowContext(ctx, `SELECT daily_from FROM usage_collection WHERE id=1`).Scan(&from); err != nil || from != "" {
					t.Fatalf("opt-out did not clear daily eligibility: %q, %v", from, err)
				}
			}
		})
	}
}
