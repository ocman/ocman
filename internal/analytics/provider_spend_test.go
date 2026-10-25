package analytics

import (
	"context"
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func spendModule(t *testing.T) *Module {
	t.Helper()
	store := statetest.Open(t, Schema)
	m := newTestModule(Deps{Store: store, TypeSafeConfigured: func(context.Context) (bool, error) { return true, nil }})
	m.subscription.authPath = filepath.Join(t.TempDir(), "missing.json")
	return m
}

func TestTypeSafeSpendSevenDays(t *testing.T) {
	m := spendModule(t)
	now := time.Now()
	input, output := int64(1_000_000), int64(100)
	for _, at := range []time.Time{now.Add(-8 * 24 * time.Hour), now.Add(-7 * 24 * time.Hour), now.Add(-time.Hour)} {
		if err := recordTypeSafeUsage(t.Context(), m.deps.Store.SQL(), at, "jev-1.14.0", &input, &output); err != nil {
			t.Fatal(err)
		}
	}
	// Seed future data directly so retention follows the actual read's clock.
	if _, err := m.deps.Store.SQL().Exec(`INSERT INTO provider_usage VALUES (?, 'jev-1.14.0', ?, ?, .042)`, now.Add(time.Hour).UnixMilli(), input, output); err != nil {
		t.Fatal(err)
	}
	// Future rows are excluded; expiry removes only rows strictly before cutoff.
	spend, err := readTypeSafeSpend(t.Context(), m.deps.Store.SQL(), now)
	if err != nil {
		t.Fatal(err)
	}
	if spend.Requests != 2 || spend.InputTokens != 2_000_000 || spend.OutputTokens != 200 || spend.EstimatedUSD == nil || math.Abs(*spend.EstimatedUSD-.084) > 1e-12 {
		t.Fatalf("spend = %+v", spend)
	}
	if spend.UnpricedRequests != 0 || spend.PeriodStart != now.Add(-7*24*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("period = %+v", spend)
	}
	var old int
	if err := m.deps.Store.SQL().QueryRow(`SELECT count(*) FROM provider_usage WHERE created_at < ?`, now.Add(-7*24*time.Hour).UnixMilli()).Scan(&old); err != nil || old != 0 {
		t.Fatalf("expired records = %d, %v", old, err)
	}
}

func TestTypeSafeSpendUnknownAndMissingUsage(t *testing.T) {
	m := spendModule(t)
	now := time.Now()
	negative, zero, input := int64(-1), int64(0), int64(100)
	for _, tc := range []struct {
		model   string
		in, out *int64
	}{
		{"unknown", &input, &input},
		{"jev-1.13.0", nil, nil},
		{"jev-1.14.0", &negative, &negative},
	} {
		if err := recordTypeSafeUsage(t.Context(), m.deps.Store.SQL(), now, tc.model, tc.in, tc.out); err != nil {
			t.Fatal(err)
		}
	}
	spend, err := readTypeSafeSpend(t.Context(), m.deps.Store.SQL(), now)
	if err != nil || spend.EstimatedUSD != nil || spend.UnpricedRequests != 3 || spend.InputTokens != 100 || spend.OutputTokens != 100 {
		t.Fatalf("unpriced spend = %+v, %v", spend, err)
	}
	if err := recordTypeSafeUsage(t.Context(), m.deps.Store.SQL(), now, "jev-1.13.0", &zero, nil); err != nil {
		t.Fatal(err)
	}
	spend, err = readTypeSafeSpend(t.Context(), m.deps.Store.SQL(), now)
	if err != nil || spend.EstimatedUSD == nil || *spend.EstimatedUSD != 0 || spend.Requests != 4 || spend.UnpricedRequests != 3 {
		t.Fatalf("partial spend = %+v, %v", spend, err)
	}
}

func TestProviderUsageRouteWithNoOAuth(t *testing.T) {
	m := spendModule(t)
	ctx := t.Context()
	input := int64(1_000_000)
	rec := serve(routes(m), http.MethodGet, "/api/provider-usage", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"TypeSafe"`) || !strings.Contains(rec.Body.String(), `"estimatedUSD":null`) {
		t.Fatalf("empty spend = %d %s", rec.Code, rec.Body.String())
	}
	if err := m.RecordTypeSafeUsage(ctx, "jev-1.14.0", &input, nil); err != nil {
		t.Fatal(err)
	}
	// The cached OAuth response must not cache or accumulate the local ledger.
	for range 2 {
		rec := serve(routes(m), http.MethodGet, "/api/provider-usage", "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"estimatedUSD":0.042`) || strings.Count(rec.Body.String(), `"id":"typesafe"`) != 1 {
			t.Fatalf("spend = %d %s", rec.Code, rec.Body.String())
		}
	}
	m.deps.TypeSafeConfigured = func(context.Context) (bool, error) { return false, nil }
	if rec := serve(routes(m), http.MethodGet, "/api/provider-usage", ""); !strings.Contains(rec.Body.String(), `"providers":[]`) {
		t.Fatalf("disabled = %s", rec.Body.String())
	}
	m.deps.TypeSafeConfigured = func(context.Context) (bool, error) { return false, errors.New("settings unavailable") }
	if rec := serve(routes(m), http.MethodGet, "/api/provider-usage", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("settings error = %d", rec.Code)
	}
}

func TestTypeSafeSpendPersistsAndErrors(t *testing.T) {
	path := statetest.Path(t)
	store, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModule(Deps{Store: store})
	input := int64(1_000_000)
	if err := m.RecordTypeSafeUsage(t.Context(), "jev-1.13.0", &input, nil); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	spend, err := readTypeSafeSpend(t.Context(), store.SQL(), time.Now())
	if err != nil || spend.Requests != 1 || spend.EstimatedUSD == nil || *spend.EstimatedUSD != .042 {
		t.Fatalf("reopened spend = %+v, %v", spend, err)
	}
	_ = store.Close()
	if err := recordTypeSafeUsage(t.Context(), store.SQL(), time.Now(), "jev-1.13.0", &input, nil); err == nil {
		t.Fatal("expected closed database error")
	}
	m = newTestModule(Deps{})
	if err := m.RecordTypeSafeUsage(t.Context(), "jev-1.13.0", &input, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTypeSafeSpendDatabaseFailures(t *testing.T) {
	m := spendModule(t)
	ctx := t.Context()
	input := int64(1)
	// A failed insert must not commit retention deletion.
	if _, err := m.deps.Store.SQL().Exec(`CREATE TRIGGER reject_usage BEFORE INSERT ON provider_usage BEGIN SELECT raise(ABORT, 'write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordTypeSafeUsage(ctx, "jev-1.13.0", &input, nil); err == nil {
		t.Fatal("expected insert error")
	}
	if _, err := m.deps.Store.SQL().Exec(`DROP TABLE provider_usage`); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordTypeSafeUsage(ctx, "jev-1.13.0", &input, nil); err == nil {
		t.Fatal("expected retention delete error")
	}
	if rec := serve(routes(m), http.MethodGet, "/api/provider-usage", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read error = %d", rec.Code)
	}
	// No configuration reader means the ledger is not accessed.
	m.deps.TypeSafeConfigured = nil
	if rec := serve(routes(m), http.MethodGet, "/api/provider-usage", ""); rec.Code != http.StatusOK {
		t.Fatalf("no configuration = %d", rec.Code)
	}
}
