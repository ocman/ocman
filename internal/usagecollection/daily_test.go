package usagecollection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/usagewire"
)

func TestStaleActivityDoesNotResetDailyEligibilityOnRestart(t *testing.T) {
	m, _ := fixture(t)
	now := m.now()
	m.now = func() time.Time { return now }
	m.deps.Daily = func(_ context.Context, day time.Time) (usagewire.Daily, error) {
		if got := day.Format(time.DateOnly); got != "2026-11-12" {
			t.Errorf("day = %s, want 2026-11-12", got)
		}
		return usagewire.Daily{Sessions: 1, Models: []usagewire.ModelUsage{{Model: "gpt-5.4", Sessions: 1}}}, nil
	}
	var ordinary, daily int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage":
			ordinary++
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/v1/usage/daily":
			daily++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error(r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	m.deps.RelayURL = func() string { return srv.URL }
	m.sendDaily(t.Context()) // Establish eligibility on Nov 11.
	now = now.AddDate(0, 0, 1)
	m.RecordActivity(t.Context())
	m.sendPending(t.Context()) // Nov 12 report fails and stays pending.
	now = now.AddDate(0, 0, 1)
	restart := NewModule(m.deps)
	restart.now = m.now
	restart.sendPending(t.Context()) // Discards the stale Nov 12 activity.
	restart.sendDaily(t.Context())
	if ordinary != 1 || daily != 1 {
		t.Fatalf("ordinary attempts = %d, daily reports = %d; want 1 of each", ordinary, daily)
	}
}

func TestDailyModelReportingLifecycle(t *testing.T) {
	m, _ := fixture(t)
	var reports []usagewire.Report
	status := http.StatusServiceUnavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage/daily" {
			t.Error(r.URL.Path)
		}
		var report usagewire.Report
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		reports = append(reports, report)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	m.deps.RelayURL = func() string { return srv.URL }
	now := m.now()
	m.now = func() time.Time { return now }
	calls := 0
	m.deps.Daily = func(_ context.Context, day time.Time) (usagewire.Daily, error) {
		calls++
		if day.Format(time.DateOnly) != "2026-11-12" || day.Hour() != 0 {
			t.Errorf("unexpected bucket %s", day)
		}
		return usagewire.Daily{Sessions: 1, Models: []usagewire.ModelUsage{{Model: "gpt-5.4", Sessions: 1, InputTokens: 4}}}, nil
	}
	m.sendDaily(t.Context()) // Nov 11: first observation of consent.
	now = now.AddDate(0, 0, 1)
	m.sendDaily(t.Context()) // Nov 12: no Nov 11 partial bucket.
	if len(reports) != 0 {
		t.Fatal("backfilled activity")
	}
	now = now.AddDate(0, 0, 1)
	m.sendDaily(t.Context())
	m.sendDaily(t.Context()) // Retry throttled.
	if len(reports) != 1 || reports[0].Daily == nil || !reports[0].Valid(now) {
		t.Fatal("missing or invalid closed-day report")
	}
	now = now.Add(15 * time.Minute)
	status = http.StatusNoContent
	m.sendDaily(t.Context())
	restart := NewModule(m.deps)
	restart.now = m.now
	restart.sendDaily(t.Context())
	if len(reports) != 2 || reports[0].InstallationID != reports[1].InstallationID {
		t.Fatal("retry or durable acknowledgement failed")
	}
	if w := setting(t, restart, "POST", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	now = now.AddDate(0, 0, 1)
	restart.sendDaily(t.Context())
	if len(reports) != 2 || calls != 2 {
		t.Fatal("read or sent daily usage after opt out")
	}
}
