package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestSubscriptionHistoryKeepsNamedAccountsSeparate(t *testing.T) {
	work := &subscriptionPlatform{id: "claudecode", account: &providerprotocol.Account{RateLimits: []providerprotocol.RateLimit{{Name: "weekly", Utilization: .25}}}}
	personal := &subscriptionPlatform{id: "claudecode", account: &providerprotocol.Account{RateLimits: []providerprotocol.RateLimit{{Name: "weekly", Utilization: .75}}}}
	sources := []SubscriptionSource{{Key: "cnx_work", Name: "Claude Work", Platform: work}, {Key: "cnx_personal", Name: "Claude Personal", Platform: personal}}
	m := historyModule(t)
	m.deps.SubscriptionSources = func(context.Context) ([]SubscriptionSource, error) { return sources, nil }
	now := time.Now().Add(-time.Minute).Truncate(subscriptionSampleInterval)
	// Old tool-level readings have no reliable account provenance.
	legacy := subscriptionProviderUsage{ID: "claudecode", Name: "Claude Code", Status: "ok", Windows: []subscriptionUsageWindow{}}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, []subscriptionProviderUsage{legacy}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := m.collectSubscriptionUsage(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	sources[0].Name = "Claude Office"
	rec := moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage/history", "", "")
	var history subscriptionUsageHistory
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &history) != nil || len(history.Samples) != 3 {
		t.Fatalf("history = %d %s", rec.Code, rec.Body.String())
	}
	for _, sample := range history.Samples {
		switch sample.Provider.ID {
		case "cnx_work":
			if sample.Provider.Name != "Claude Office" || sample.Provider.Windows[0].UsedPercent != 25 || sample.Provider.MachineName != "This machine" {
				t.Fatalf("work = %+v", sample.Provider)
			}
		case "cnx_personal":
			if sample.Provider.Name != "Claude Personal" || sample.Provider.Windows[0].UsedPercent != 75 {
				t.Fatalf("personal = %+v", sample.Provider)
			}
		case "claudecode":
			if sample.Provider.Name != "Claude Code" {
				t.Fatalf("legacy reading reassigned: %+v", sample.Provider)
			}
		default:
			t.Fatalf("unexpected account: %+v", sample.Provider)
		}
	}
	// Removing a connection from the current inventory does not erase its history.
	sources = sources[1:]
	rec = moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage/history", "", "")
	if !strings.Contains(rec.Body.String(), `"name":"Claude Work"`) || work.calls != 2 || personal.calls != 2 {
		t.Fatalf("retained history = %s, calls %d/%d", rec.Body.String(), work.calls, personal.calls)
	}
}

func TestSubscriptionConnectionInventoryFailureIsPrivate(t *testing.T) {
	m := historyModule(t)
	m.deps.SubscriptionSources = func(context.Context) ([]SubscriptionSource, error) {
		return nil, errors.New("private connection store detail")
	}
	if err := m.collectSubscriptionUsage(t.Context(), time.Now()); err == nil {
		t.Fatal("sampler ignored connection inventory failure")
	}
	for _, path := range []string{"/api/provider-usage", "/api/provider-usage/history"} {
		rec := moduletest.Do(routes(m), http.MethodGet, path, "", "")
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "private connection") {
			t.Fatalf("inventory error = %d %s", rec.Code, rec.Body.String())
		}
	}
}
