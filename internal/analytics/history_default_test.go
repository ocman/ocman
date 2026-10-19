package analytics

import (
	"net/http"
	"testing"
)

func TestMissingHistoryNeverSelectsLegacyAnalytics(t *testing.T) {
	f := newFixture(t)
	for _, route := range conversationAnalyticsRoutes {
		rec := serve(routes(f.Module), http.MethodGet, route, "")
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
			t.Fatalf("%s selected legacy without history: %d %s", route, rec.Code, rec.Body)
		}
	}
}
