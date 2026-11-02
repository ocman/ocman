package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func historyModule(t *testing.T, adapters ...platforms.Platform) *Module {
	t.Helper()
	return NewModule(Deps{Store: statetest.Open(t, Schema), Platforms: func() []platforms.Platform { return adapters }})
}

func TestCollectSubscriptionUsageEveryPlatform(t *testing.T) {
	p := &subscriptionPlatform{id: "codex", account: &providerprotocol.Account{SubscriptionType: "pro", RateLimits: []providerprotocol.RateLimit{{Name: "weekly", Utilization: .4, ResetsAt: 1793602742000}}}}
	q := &subscriptionPlatform{id: "claudecode", err: errors.New("private token must not persist")}
	o := &subscriptionPlatform{id: "opencode", account: &providerprotocol.Account{Budgets: []platforms.ProviderBudget{{ID: "budget", Name: "Monthly", LimitUSD: 100, SpentUSD: 25, RemainingUSD: 75}}}}
	remote := &subscriptionPlatform{id: "r-owner:codex", account: p.account}
	m := historyModule(t, p, q, o, remote, &subscriptionPlatform{id: "unsupported", err: platforms.ErrUnsupported}, &noAccountPlatform{})
	now := time.Now().Truncate(subscriptionSampleInterval)
	for range 2 {
		if err := m.collectSubscriptionUsage(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	history, err := readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), now, 7)
	if err != nil || len(history.Samples) != 3 || p.calls != 2 || remote.calls != 0 || history.SampleIntervalMs != 300000 {
		t.Fatalf("history=%+v err=%v calls=%d/%d", history, err, p.calls, remote.calls)
	}
	for _, sample := range history.Samples {
		if sample.SampledAt != now.UnixMilli() {
			t.Fatalf("timestamp=%d", sample.SampledAt)
		}
		switch sample.Provider.ID {
		case "codex":
			if sample.Provider.Plan != "pro" || sample.Provider.Windows[0].UsedPercent != 40 || sample.Provider.Windows[0].ResetsAt == "" {
				t.Fatalf("codex=%+v", sample)
			}
		case "claudecode":
			if sample.Provider.Status != "upstream_error" || len(sample.Provider.Windows) != 0 {
				t.Fatalf("failure is not a gap: %+v", sample)
			}
		case "opencode":
			if sample.Provider.Budgets[0].RemainingUSD != 75 {
				t.Fatalf("budgets=%+v", sample)
			}
		}
	}
	body, _ := json.Marshal(history)
	if strings.Contains(string(body), "private token") {
		t.Fatal("persisted platform error details")
	}
	// A later failed probe is a separate missing observation, not cached success.
	p.err = platforms.ErrPlatformUnreachable
	if err := m.collectSubscriptionUsage(t.Context(), now.Add(subscriptionSampleInterval)); err != nil {
		t.Fatal(err)
	}
	history, _ = readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), now.Add(subscriptionSampleInterval), 7)
	if len(history.Samples) != 6 || history.Samples[4].Provider.Status != "upstream_error" {
		t.Fatalf("later failure=%+v", history)
	}
}

func TestSubscriptionHistoryRetentionAndReopen(t *testing.T) {
	path := statetest.Path(t)
	store, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(subscriptionSampleInterval)
	p := subscriptionProviderUsage{ID: "codex", Name: "Codex", Status: "ok", Windows: []subscriptionUsageWindow{}, Spend: &providerSpend{Requests: 2}}
	for _, at := range []time.Time{now.Add(-31 * 24 * time.Hour), now.Add(-2 * 24 * time.Hour), now.Add(-time.Hour), now.Add(time.Hour)} {
		if err := recordSubscriptionUsage(t.Context(), store.SQL(), at, []subscriptionProviderUsage{p}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recordSubscriptionUsage(t.Context(), store.SQL(), now, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for days, count := range map[int]int{0: 3, 1: 1, 7: 2, 30: 2, 90: 3} {
		history, err := readSubscriptionUsageHistory(t.Context(), store.SQL(), now, days)
		if err != nil || len(history.Samples) != count || history.Samples[0].Provider.Spend != nil {
			t.Fatalf("days=%d history=%+v err=%v", days, history, err)
		}
	}
	var count int
	if err := store.SQL().QueryRow(`SELECT count(*) FROM subscription_usage_samples WHERE sampled_at < ?`, now.Add(-subscriptionHistoryFineRetention).UnixMilli()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retention count=%d err=%v", count, err)
	}
}

func TestSubscriptionHistoryMigrationPreservesLedger(t *testing.T) {
	old := Schema
	old.Tables = []string{"provider_usage"}
	old.Migrations = old.Migrations[:1]
	path := statetest.Path(t)
	store, err := state.Open(path, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQL().Exec(`INSERT INTO provider_usage VALUES (1, 'jev-1.14.0', 100, 1, .042)`); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.SQL().QueryRow(`SELECT count(*) FROM provider_usage`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("old ledger lost: %d %v", count, err)
	}
	if err := recordSubscriptionUsage(t.Context(), store.SQL(), time.Now(), nil); err != nil {
		t.Fatal(err)
	}
}

type samplingPlatform struct {
	*subscriptionPlatform
	probe func(context.Context) (*providerprotocol.Account, error)
}

func (p *samplingPlatform) Account(ctx context.Context) (*providerprotocol.Account, error) {
	return p.probe(ctx)
}

func TestSubscriptionSamplerRunsImmediatelyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	p := &samplingPlatform{subscriptionPlatform: &subscriptionPlatform{id: "codex"}, probe: func(ctx context.Context) (*providerprotocol.Account, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("probe has no deadline")
		}
		if calls == 2 {
			cancel()
			return nil, ctx.Err()
		}
		return &providerprotocol.Account{}, nil
	}}
	m := historyModule(t, p)
	m.subscriptionSampleEvery = time.Millisecond
	done := make(chan struct{})
	go func() { defer close(done); m.runSubscriptionUsageLoop(ctx) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sampler did not stop")
	}
	history, err := readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), time.Now(), 7)
	if err != nil || calls != 2 || len(history.Samples) != 1 {
		t.Fatalf("calls=%d history=%+v err=%v", calls, history, err)
	}
	NewModule(Deps{}).runSubscriptionUsageLoop(t.Context())
	if err := NewModule(Deps{}).collectSubscriptionUsage(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionHistoryRoute(t *testing.T) {
	m := historyModule(t)
	for _, days := range []string{"", "0", "1", "7", "30", "90", "365"} {
		rec := moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage/history?days="+days, "", "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"samples":[]`) {
			t.Fatalf("empty history=%d %s", rec.Code, rec.Body.String())
		}
	}
	for _, days := range []string{"-1", "366", "invalid"} {
		if rec := moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage/history?days="+days, "", ""); rec.Code != 400 {
			t.Fatalf("days=%s status=%d", days, rec.Code)
		}
	}
	if rec := moduletest.Do(routes(NewModule(Deps{})), http.MethodGet, "/api/provider-usage/history", "", ""); rec.Code != 503 {
		t.Fatalf("no store=%d", rec.Code)
	}
	if rec := moduletest.Do(routes(m), http.MethodPost, "/api/provider-usage/history", "", ""); rec.Code != 405 {
		t.Fatalf("wrong method=%d", rec.Code)
	}
	if rec := moduletest.Do(routesWith(m, module.Guards{Localhost: loopbackOnly}), http.MethodGet, "/api/provider-usage/history", "", ""); rec.Code != 403 {
		t.Fatalf("nonlocal=%d", rec.Code)
	}
}

func TestSubscriptionHistoryFailuresRollbackAndStayPrivate(t *testing.T) {
	m := historyModule(t)
	now := time.Now().Truncate(subscriptionSampleInterval)
	old := now.Add(-31 * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	if _, err := m.deps.Store.SQL().Exec(`INSERT INTO subscription_usage_samples VALUES ('old', ?, '{}'), ('old', ?, '{}')`, old, old+subscriptionSampleInterval.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	provider := subscriptionProviderUsage{ID: "codex", Windows: []subscriptionUsageWindow{{UsedPercent: math.NaN()}}}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, []subscriptionProviderUsage{provider}); err == nil {
		t.Fatal("invalid numeric data accepted")
	}
	var count int
	if err := m.deps.Store.SQL().QueryRow(`SELECT count(*) FROM subscription_usage_samples`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed write did not roll back retention: %d %v", count, err)
	}
	provider.Windows = nil
	if _, err := m.deps.Store.SQL().Exec(`CREATE TRIGGER reject_subscription BEFORE INSERT ON subscription_usage_samples BEGIN SELECT raise(ABORT, 'private database detail'); END`); err != nil {
		t.Fatal(err)
	}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, []subscriptionProviderUsage{provider}); err == nil {
		t.Fatal("failed insert accepted")
	}
	if _, err := m.deps.Store.SQL().Exec(`DROP TRIGGER reject_subscription; DROP TABLE subscription_usage_samples`); err != nil {
		t.Fatal(err)
	}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, nil); err == nil {
		t.Fatal("failed retention accepted")
	}
	rec := moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage/history", "", "")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "subscription_usage_samples") {
		t.Fatalf("private history error=%d %s", rec.Code, rec.Body.String())
	}
	if err := m.deps.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recordSubscriptionUsage(t.Context(), m.deps.Store.SQL(), now, nil); err == nil {
		t.Fatal("closed store accepted")
	}
}

func TestSubscriptionHistoryRejectsCorruptSamples(t *testing.T) {
	m := historyModule(t)
	if _, err := m.deps.Store.SQL().Exec(`INSERT INTO subscription_usage_samples VALUES ('codex', ?, 'not-json')`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := readSubscriptionUsageHistory(t.Context(), m.deps.Store.SQL(), time.Now(), 7); err == nil {
		t.Fatal("corrupt sample accepted")
	}
}
