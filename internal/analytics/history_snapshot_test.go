package analytics

import (
	"net/http"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/statev2"
)

// A commit landing after scope validation must not reach the response built
// from it: the colliding native session ID would merge two sources' sessions.
func TestHistoryResponseReadsTheValidatedSnapshot(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	if err := h.Store.RegisterInstance(t.Context(), statev2.Instance{ID: "two", OwnerID: "owner", SourceKey: "other", Platform: "test"}); err != nil {
		t.Fatal(err)
	}
	h.Instances = func() ([]string, bool) { return []string{"one", "two"}, true }
	const path = "/api/metric-logs?kind=session"
	var before db.MetricsLog
	decode(t, f.serve(http.MethodGet, path, ""), &before)
	committed := false
	h.validated = func() {
		if committed {
			return
		}
		committed = true
		now := time.Now().UnixMilli()
		if _, err := h.Store.Commit(t.Context(), statev2.Batch{
			Session: statev2.Session{Key: statev2.Key{InstanceID: "two", SessionID: "root"}, Title: "Foreign", Directory: "/repo"},
			Messages: []statev2.Message{{ID: "foreign", Role: "assistant", Provider: "p", Model: "org/model", CreatedAt: now, CompletedAt: now + 1000,
				Cost: 9, TokensInput: 1000, TokensOutput: 1000, Finish: "stop"}},
		}); err != nil {
			t.Error(err)
		}
	}
	rec := f.serve(http.MethodGet, path, "")
	if rec.Code == http.StatusOK {
		var got db.MetricsLog
		decode(t, rec, &got)
		for _, s := range got.Sessions {
			if s.ID == "root" && (s.Requests != sessionRequests(before, "root") || s.Cost != sessionCost(before, "root")) {
				t.Fatalf("merged a colliding source committed after validation: %+v", s)
			}
		}
	} else if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
	rec = f.serve(http.MethodGet, path, "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-Ocman-Analytics-Error") != "history-scope-error" {
		t.Fatalf("collision not rejected on the next read: %d %v", rec.Code, rec.Header())
	}
}

func sessionRequests(log db.MetricsLog, id string) int {
	for _, s := range log.Sessions {
		if s.ID == id {
			return s.Requests
		}
	}
	return 0
}

func sessionCost(log db.MetricsLog, id string) float64 {
	for _, s := range log.Sessions {
		if s.ID == id {
			return s.Cost
		}
	}
	return 0
}
