package analytics

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestEveryAnalyticsRouteServesCommittedPartialHistory(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	var complete db.Stats
	decode(t, f.serve(http.MethodGet, "/api/stats", ""), &complete)
	h.Instances = func() ([]string, bool) { return []string{"one"}, false }
	f.legacy.Close()
	f.legacy = nil
	for _, route := range conversationAnalyticsRoutes {
		t.Run(route, func(t *testing.T) {
			rec := f.serve(http.MethodGet, route, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("one incomplete source blocked committed metrics: %d %s", rec.Code, rec.Body)
			}
			if rec.Header().Get("X-Ocman-Analytics-Partial") != "true" || rec.Header().Get("X-Ocman-Analytics-Error") != "" {
				t.Fatalf("partial history not identified as usable: %v", rec.Header())
			}
		})
	}
	var partial db.Stats
	decode(t, f.serve(http.MethodGet, "/api/stats", ""), &partial)
	if complete.TotalSessions == 0 || !reflect.DeepEqual(complete, partial) {
		t.Fatalf("incomplete ingestion discarded committed metrics: complete=%+v partial=%+v", complete, partial)
	}
}
