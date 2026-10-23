package analytics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Import native snapshots and exercise the stored-history HTTP routes.
func analyticsParityFixture(t *testing.T) (*fixture, *HistoryAnalytics, time.Time) {
	return analyticsSummaryFixture(t, nil)
}

func analyticsSummaryFixture(t *testing.T, seed func(*fixture, time.Time)) (*fixture, *HistoryAnalytics, time.Time) {
	t.Helper()
	f := newFixture(t)
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1).UnixMilli()
	today := now.UnixMilli()
	for _, s := range []struct{ id, title, dir string }{
		{"root", "Root", "/repo"}, {"child", "Task (@explore subagent)", "/repo/wt"}, {"other", "Other", "/repo2"},
	} {
		if _, err := f.raw.Exec(`INSERT INTO session(id,title,directory,time_created,time_updated) VALUES(?,?,?,?,?)`, s.id, s.title, s.dir, yesterday, yesterday); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []struct {
		id, session, data string
		at                int64
	}{
		{"u", "root", `{"role":"user"}`, yesterday},
		{"a", "root", `{"role":"assistant","providerID":"p","modelID":"org/model"}`, yesterday},
		{"today", "root", `{"role":"assistant","providerID":"p","modelID":"org/model"}`, today},
		{"cu", "child", `{"role":"user"}`, yesterday},
		{"ca", "child", `{"role":"assistant","providerID":"p","modelID":"org/model"}`, yesterday},
		{"oa", "other", `{"role":"assistant","providerID":"q","modelID":"other"}`, yesterday},
	} {
		if _, err := f.raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES(?,?,?,?)`, m.id, m.session, m.at, m.data); err != nil {
			t.Fatal(err)
		}
	}
	// Exercise pricing, finish/error classification, per-agent filters and tool
	// timing subtraction. The large output must not reach the compact projection.
	data := fmt.Sprintf(`{"role":"assistant","agent":"build","providerID":"p","modelID":"org/model","cost":0.25,"tokens":{"input":100,"output":20,"cache":{"read":5,"write":2}},"time":{"created":%d,"completed":%d},"finish":"stop"}`, yesterday, yesterday+10000)
	if _, err := f.raw.Exec(`UPDATE message SET data=? WHERE id='a'`, data); err != nil {
		t.Fatal(err)
	}
	timing := fmt.Sprintf(`{"type":"tool","tool":"question","state":{"time":{"start":%d,"end":%d},"output":"large output"}}`, yesterday+1000, yesterday+3000)
	if _, err := f.raw.Exec(`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES('tool','a','root',?,?)`, yesterday, timing); err != nil {
		t.Fatal(err)
	}
	if seed != nil {
		seed(f, now)
	}
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "one", OwnerID: "owner", SourceKey: "test", Platform: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"root", "child", "other"} {
		snapshot, err := f.legacy.HistorySnapshot(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "one", SessionID: id}, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	h := &HistoryAnalytics{Store: store, Instances: func() ([]string, bool) { return []string{"one"}, true }}
	f.deps.HistoryAnalytics = func() *HistoryAnalytics { return h }
	f.mux = routes(f.Module)
	t.Cleanup(f.Close)
	return f, h, now
}

func TestActivityReadsStoredHistoryWithFiltersAndHeaders(t *testing.T) {
	f, h, now := analyticsParityFixture(t)
	today := now.UnixMilli()
	for _, q := range []string{"", "?model=" + url.QueryEscape("p/org/model"), "?model=" + url.QueryEscape("org/model"), "?model=missing", "?dir=" + url.QueryEscape("/repo"), "?dir=/repo/wt", "?since=" + strconv.FormatInt(today, 10), "?dir=/repo&model=org%2Fmodel"} {
		t.Run(q, func(t *testing.T) {
			rec := f.serve(http.MethodGet, "/api/activity"+q, "")
			var got []db.DailyActivity
			decode(t, rec, &got)
			messages := 0
			for _, day := range got {
				messages += day.Messages
			}
			want := map[string]int{"": 4, "?model=p%2Forg%2Fmodel": 3, "?model=org%2Fmodel": 3,
				"?model=missing": 0, "?dir=%2Frepo": 3, "?dir=/repo/wt": 1,
				"?since=" + strconv.FormatInt(today, 10): 4, "?dir=/repo&model=org%2Fmodel": 3}[q]
			if messages != want || len(got) != 366 {
				t.Fatalf("%s: messages=%d want=%d days=%d", q, messages, want, len(got))
			}
			if rec.Header().Get("X-Ocman-Activity-Source") != "statev2" || rec.Header().Get("X-Ocman-Activity-Fallback") != "" {
				t.Fatal(rec.Header())
			}
		})
	}
	h.Instances = func() ([]string, bool) { return nil, false }
	rec := f.serve(http.MethodGet, "/api/activity", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-Ocman-Analytics-Error") != "import-incomplete" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	// Ensure the response remains the original JSON array, not a new envelope.
	h.Instances = func() ([]string, bool) { return []string{"one"}, true }
	rec = f.serve(http.MethodGet, "/api/activity", "")
	var rows []db.DailyActivity
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 366 {
		t.Fatalf("response shape: %v", err)
	}
}
