package analytics

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// The legacy reader buckets with SQLite's localtime, whose timezone is fixed
// when the process starts (on Linux the C library reads its own copy of TZ),
// so each zone runs in a child test process started with that TZ.
func TestHistorySummaryExactCutoffsAndLocalBuckets(t *testing.T) {
	const childEnv = "OCMAN_TEST_SUMMARY_ZONE"
	if zone := os.Getenv(childEnv); zone != "" {
		testSummaryCutoffsInZone(t, zone)
		return
	}
	for _, zone := range []string{"UTC", "Asia/Kathmandu", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHistorySummaryExactCutoffsAndLocalBuckets$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), "TZ="+zone, childEnv+"="+zone)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		})
	}
}

func testSummaryCutoffsInZone(t *testing.T, zone string) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, want := time.Now().In(loc).Zone(); func() int { _, got := time.Now().Zone(); return got }() != want {
		t.Fatalf("child process does not run in %s", zone)
	}
	var boundary int64
	f, h, _ := analyticsSummaryFixture(t, func(f *fixture, now time.Time) {
		base := now.Add(-48 * time.Hour).UTC().Truncate(time.Hour)
		boundary = base.Add(15 * time.Minute).UnixMilli()
		for i, offset := range []time.Duration{0, 14 * time.Minute, 15 * time.Minute, 44 * time.Minute, 59 * time.Minute, time.Hour} {
			at := base.Add(offset).UnixMilli()
			data := fmt.Sprintf(`{"role":"assistant","agent":" build ","modelID":"ignored","model":{"providerID":"nested","modelID":" model "},"tokens":{"input":11,"output":7,"cache":{"read":3,"write":1}},"time":{"created":%d,"completed":%d},"finish":" stop "}`, at, at+10000)
			if _, err := f.raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES(?,?,?,?)`, fmt.Sprintf("boundary-%d", i), "root", at, data); err != nil {
				t.Fatal(err)
			}
		}
		// Both UTC hours map to the repeated 01:00 hour in New York.
		nov := time.Date(now.Year()-1, time.November, 1, 0, 0, 0, 0, time.UTC)
		nov = nov.AddDate(0, 0, (7-int(nov.Weekday()))%7)
		for i, offset := range []time.Duration{5*time.Hour + 30*time.Minute, 6*time.Hour + 30*time.Minute} {
			at := nov.Add(offset).UnixMilli()
			if _, err := f.raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES(?,?,?,?)`, fmt.Sprintf("dst-%d", i), "root", at, `{"role":"assistant","providerID":"p","modelID":"dst","tokens":{"input":9,"output":4}}`); err != nil {
				t.Fatal(err)
			}
		}
	})
	// The routes derive cutoffs from whole days only, so compare the
	// readers directly at the partial-hour boundary.
	t.Run("boundary", func(t *testing.T) {
		history, release, reason := h.projection(t.Context())
		if history == nil {
			t.Fatal(reason)
		}
		defer release()
		compareSummaryReaders(t, f.legacy, history, boundary)
	})
	for _, route := range []string{
		"/api/hourly-tokens?days=7&model=nested%2F%20model%20",
		"/api/hourly-tokens?days=730&model=dst",
		"/api/activity?model=nested%2F%20model%20&dir=/repo",
		"/api/metrics?days=7", "/api/metrics/performance?days=7&agent=build", "/api/metric-logs?kind=request&days=7&offset=1&limit=2",
	} {
		t.Run(route, func(t *testing.T) {
			before := f.serveLegacy(http.MethodGet, route, "")
			var want any
			decode(t, before, &want)
			after := f.serve(http.MethodGet, route, "")
			var got any
			decode(t, after, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("summary parity differs: %s\nlegacy=%s\nsummary=%s", route, before.Body, after.Body)
			}
		})
	}
}

// compareSummaryReaders checks that the legacy and summary readers agree at
// an exact cutoff 15 minutes into an hour, and that the cutoff is inclusive:
// of the six boundary messages, those at +0 and +14 minutes are excluded.
func compareSummaryReaders(t *testing.T, legacy, history *db.DB, boundary int64) {
	t.Helper()
	ctx := t.Context()
	for name, read := range map[string]func(*db.DB) (any, error){
		"activity": func(r *db.DB) (any, error) { return r.GetDailyActivity(ctx, boundary, "", "") },
		"models":   func(r *db.DB) (any, error) { return r.GetModelUsage(ctx, boundary, "") },
		"hourly":   func(r *db.DB) (any, error) { return r.GetHourlyTokensByModel(ctx, 7, boundary, "", "") },
	} {
		want, err := read(legacy)
		if err != nil {
			t.Fatal(err)
		}
		got, err := read(history)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s differs at the boundary\nlegacy=%+v\nsummary=%+v", name, want, got)
		}
	}
	for _, r := range []*db.DB{legacy, history} {
		models, err := r.GetModelUsage(ctx, boundary, "")
		if err != nil {
			t.Fatal(err)
		}
		var count int
		var in int64
		for _, m := range models {
			if m.Provider == "nested" {
				count, in = count+m.Count, in+m.TokensIn
			}
		}
		if count != 4 || in != 44 {
			t.Fatalf("boundary messages included = %d (%d tokens), want +15, +44, +59 and +60 minutes: %+v", count, in, models)
		}
		hours, err := r.GetHourlyTokensByModel(ctx, 7, boundary, "", "")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]int64{}
		for _, offset := range []time.Duration{0, 29 * time.Minute, 44 * time.Minute, 45 * time.Minute} {
			want[time.UnixMilli(boundary).Add(offset).Format("2006-01-02 15")] += 11
		}
		got := map[string]int64{}
		for _, hour := range hours {
			if hour.Provider == "nested" {
				got[hour.Datetime] += hour.TokensIn
			}
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("boundary hours = %v, want local hours of +15, +44, +59 and +60 minutes: %v", got, want)
		}
	}
}
