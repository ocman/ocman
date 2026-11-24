package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type delayedUsagePlatform struct {
	subscriptionPlatform
	fail       bool
	delayFirst bool
}

func (p *delayedUsagePlatform) Account(ctx context.Context) (*providerprotocol.Account, error) {
	p.calls++
	if p.calls != 1 || p.delayFirst {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	if p.fail {
		return nil, platforms.ErrPlatformUnreachable
	}
	return p.account, nil
}

func TestProviderUsageSharesRefreshAndServesLastGood(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &delayedUsagePlatform{subscriptionPlatform: subscriptionPlatform{id: "slow", account: &providerprotocol.Account{SubscriptionType: "pro"}}}
		m := newTestModule(Deps{Platforms: func() []platforms.Platform { return []platforms.Platform{p} }})
		defer m.Finalize()
		mux := routes(m)
		read := func() subscriptionUsageResponse {
			t.Helper()
			r := moduletest.Do(mux, http.MethodGet, "/api/provider-usage", "", "")
			var snapshot subscriptionUsageResponse
			if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &snapshot) != nil || len(snapshot.Providers) != 1 {
				t.Fatalf("usage = %d %s", r.Code, r.Body.String())
			}
			return snapshot
		}
		if got := read().Providers[0]; got.Plan != "pro" {
			t.Fatalf("prime = %+v", got)
		}
		time.Sleep(time.Minute)
		p.fail = true
		start := time.Now()
		if got := read().Providers[0]; got.Plan != "pro" || got.RefreshedAt == "" || time.Since(start) > time.Second {
			t.Errorf("stale response = %+v after %v", got, time.Since(start))
		}
		if got := read().Providers[0]; got.Plan != "pro" || time.Since(start) > 2*time.Second {
			t.Errorf("second caller did not share running refresh: %+v after %v", got, time.Since(start))
		}
		time.Sleep(3 * time.Second)
		if got := read().Providers[0]; got.Plan != "pro" || p.calls != 2 {
			t.Errorf("failed refresh replaced cache or retried early: %+v, calls=%d", got, p.calls)
		}
	})
}

func TestProviderUsageColdCacheReturnsReadyProviders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		slow := &delayedUsagePlatform{delayFirst: true, subscriptionPlatform: subscriptionPlatform{id: "slow", account: &providerprotocol.Account{SubscriptionType: "pro"}}}
		fast := &subscriptionPlatform{id: "fast", account: &providerprotocol.Account{SubscriptionType: "max"}}
		m := newTestModule(Deps{Platforms: func() []platforms.Platform { return []platforms.Platform{slow, fast} }})
		defer m.Finalize()
		mux := routes(m)
		start := time.Now()
		r := moduletest.Do(mux, http.MethodGet, "/api/provider-usage", "", "")
		var got subscriptionUsageResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &got) != nil || len(got.Providers) != 2 {
			t.Fatalf("cold response = %d %s", r.Code, r.Body.String())
		}
		if time.Since(start) > time.Second || got.Providers[0].Status != "upstream_error" || got.Providers[0].RefreshedAt != "" || got.Providers[1].Plan != "max" {
			t.Fatalf("cold response after %v = %+v", time.Since(start), got)
		}
		time.Sleep(3 * time.Second)
		r = moduletest.Do(mux, http.MethodGet, "/api/provider-usage", "", "")
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"plan":"pro"`) || slow.calls != 1 {
			t.Fatalf("refresh did not finish after response: %d %s, calls=%d", r.Code, r.Body.String(), slow.calls)
		}
	})
}

type subscriptionPlatform struct {
	platforms.Platform
	id      platforms.ID
	account *providerprotocol.Account
	err     error
	calls   int
}

func TestProviderUsageDoesNotReadCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.Mkdir(filepath.Join(dir, "opencode"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{`{"openai":{"type":"oauth","access":"secret"},"anthropic":{"type":"oauth","access":"secret"}}`, `broken json`} {
		if err := os.WriteFile(filepath.Join(dir, "opencode", "auth.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		m := newTestModule(Deps{})
		rec := moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage", "", "")
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"providers":[]}` {
			t.Fatalf("unexpected credentials fallback: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestPlatformSubscriptionFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		status    string
		supported bool
	}{
		{"unsupported", platforms.ErrUnsupported, "upstream_error", false},
		{"signed out", platforms.ErrNotFound, "signed_out", true},
		{"unauthenticated", platforms.ErrPlatformUnauthenticated, "unauthorized", true},
		{"unreachable", platforms.ErrPlatformUnreachable, "upstream_error", true},
		{"private error", errors.New("secret credentials"), "upstream_error", true},
		{"nil account", nil, "upstream_error", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &subscriptionPlatform{id: "codex", err: tc.err}
			got, supported := fetchPlatformSubscription(t.Context(), p, p)
			if got.Status != tc.status || supported != tc.supported || got.Windows == nil {
				t.Fatalf("usage = %+v, supported=%v", got, supported)
			}
			body, _ := json.Marshal(got)
			if strings.Contains(string(body), "secret") {
				t.Fatal("error exposed credentials")
			}
		})
	}
}

func TestPlatformUsageCacheAndRegistryChanges(t *testing.T) {
	p := &subscriptionPlatform{id: "codex", account: &providerprotocol.Account{SubscriptionType: "pro"}}
	q := &subscriptionPlatform{id: "claudecode", account: &providerprotocol.Account{SubscriptionType: "max"}}
	unsupported := &subscriptionPlatform{id: "unsupported", err: platforms.ErrUnsupported}
	var cache subscriptionUsageCache
	defer cache.workers.Close()
	for range 3 {
		got := cache.get(t.Context(), usageSources(p, unsupported), nil)
		if len(got.Providers) != 1 {
			t.Fatalf("providers = %+v", got)
		}
	}
	if p.calls != 1 {
		t.Fatalf("calls=%d", p.calls)
	}
	cache.entries[p.id].next = time.Now().Add(-time.Second)
	got := cache.get(t.Context(), usageSources(p, q), nil)
	if p.calls != 2 || q.calls != 1 || len(got.Providers) != 2 {
		t.Fatalf("refresh = %+v, calls=%d/%d", got, p.calls, q.calls)
	}
	// Removed adapters must not survive in either the response or cache.
	got = cache.get(t.Context(), usageSources(q), nil)
	if len(got.Providers) != 1 || got.Providers[0].ID != "claudecode" || len(cache.entries) != 1 {
		t.Fatalf("registry change = %+v", got)
	}
	// Platforms without account support never need credentials or another app.
	got = cache.get(t.Context(), usageSources(&noAccountPlatform{}), nil)
	if len(got.Providers) != 0 || len(cache.entries) != 0 {
		t.Fatalf("unsupported = %+v", got)
	}
}

type noAccountPlatform struct{ platforms.Platform }

func (*noAccountPlatform) ID() platforms.ID    { return "no-account" }
func (*noAccountPlatform) DisplayName() string { return "No account" }

func TestPlatformUsageDoesNotStartRefreshForCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := &subscriptionPlatform{id: "codex", err: context.Canceled}
	var cache subscriptionUsageCache
	defer cache.workers.Close()
	cache.get(ctx, usageSources(p), nil)
	if p.calls != 0 {
		t.Fatal("canceled request started an account read")
	}
	p.err, p.account = nil, &providerprotocol.Account{SubscriptionType: "pro"}
	got := cache.get(t.Context(), usageSources(p), nil)
	if p.calls != 1 || got.Providers[0].Status != "ok" {
		t.Fatalf("retry=%+v, calls=%d", got, p.calls)
	}
}

func TestPlatformSubscriptionWindowsAndBudgets(t *testing.T) {
	p := &subscriptionPlatform{id: "opencode", account: &providerprotocol.Account{
		RateLimits: []providerprotocol.RateLimit{{Name: "five_hour", Utilization: -.5}, {Name: "custom", Utilization: 2, ResetsAt: -1}},
		Budgets:    []platforms.ProviderBudget{{Name: "Monthly", LimitUSD: 100, SpentUSD: 25, RemainingUSD: 75}},
	}}
	got, _ := fetchPlatformSubscription(t.Context(), p, p)
	if got.Windows[0].Name != "5 hours" || got.Windows[0].UsedPercent != 0 || got.Windows[1].Name != "custom" || got.Windows[1].UsedPercent != 100 || got.Windows[1].ResetsAt != "" || got.Budgets[0].RemainingUSD != 75 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestPlatformSubscriptionRouteGuards(t *testing.T) {
	mux := routesWith(newTestModule(Deps{}), module.Guards{Localhost: loopbackOnly})
	for _, tc := range []struct {
		method, remote string
		status         int
	}{
		{http.MethodGet, "127.0.0.1:1234", http.StatusOK},
		{http.MethodPost, "127.0.0.1:1234", http.StatusMethodNotAllowed},
		{http.MethodGet, "10.0.0.1:1234", http.StatusForbidden},
	} {
		req := httptest.NewRequest(tc.method, "/api/provider-usage", nil)
		req.RemoteAddr = tc.remote
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s %s = %d", tc.method, tc.remote, rec.Code)
		}
	}
}

func (p *subscriptionPlatform) ID() platforms.ID    { return p.id }
func (p *subscriptionPlatform) DisplayName() string { return string(p.id) }
func (p *subscriptionPlatform) Account(context.Context) (*providerprotocol.Account, error) {
	p.calls++
	return p.account, p.err
}

func usageSources(adapters ...platforms.Platform) []SubscriptionSource {
	sources := make([]SubscriptionSource, 0, len(adapters))
	for _, p := range adapters {
		sources = append(sources, SubscriptionSource{Key: p.ID(), Name: p.DisplayName(), Platform: p})
	}
	return sources
}

func TestProviderUsageReadsPlatformAccount(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	p := &subscriptionPlatform{id: "codex", account: &providerprotocol.Account{
		SubscriptionType: "pro", RateLimits: []providerprotocol.RateLimit{{Name: "weekly", Utilization: .25, ResetsAt: 1793602742000}},
	}}
	m := newTestModule(Deps{Platforms: func() []platforms.Platform { return []platforms.Platform{p} }})
	rec := moduletest.Do(routes(m), http.MethodGet, "/api/provider-usage", "", "")
	var got subscriptionUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(got.Providers) != 1 || p.calls != 1 {
		t.Fatalf("platform usage = %d %s, calls=%d", rec.Code, rec.Body.String(), p.calls)
	}
	if got.Providers[0].Plan != "pro" || got.Providers[0].Windows[0].UsedPercent != 25 || got.Providers[0].Windows[0].ResetsAt != "2026-11-02T06:59:02Z" {
		t.Fatalf("usage = %+v", got.Providers[0])
	}
}
