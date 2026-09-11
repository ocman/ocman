package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSubscriptionUsageRoute(t *testing.T) {
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer openai-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "account-secret" {
			t.Fatalf("ChatGPT-Account-Id = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"plan_type":"pro",
			"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_at":1789450296}},
			"additional_rate_limits":[{"limit_name":"Codex Spark","rate_limit":{"primary_window":{"used_percent":45,"limit_window_seconds":18000,"reset_at":1789144348}}}]
		}`))
	}))
	defer openAI.Close()

	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer anthropic-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Fatalf("anthropic-beta = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"five_hour":{"utilization":1,"resets_at":"2026-09-11T16:10:00Z"},
			"seven_day":{"utilization":23,"resets_at":"2026-09-16T08:00:00Z"},
			"seven_day_opus":null
		}`))
	}))
	defer anthropic.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"openai":{"type":"oauth","access":"openai-secret","expires":4102444800000,"accountId":"account-secret"},
		"anthropic":{"type":"oauth","access":"anthropic-secret","expires":4102444800000}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := testServer(t)
	srv.subscriptionUsage = subscriptionUsageClient{
		http:         http.DefaultClient,
		authPath:     authPath,
		openAIURL:    openAI.URL,
		anthropicURL: anthropic.URL,
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/subscription-usage", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var got subscriptionUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 2 || got.Providers[0].ID != "openai" || got.Providers[0].Plan != "pro" {
		t.Fatalf("providers = %#v", got.Providers)
	}
	if len(got.Providers[0].Windows) != 2 || got.Providers[0].Windows[1].Name != "Codex Spark · 5 hours" {
		t.Fatalf("OpenAI windows = %#v", got.Providers[0].Windows)
	}
	if len(got.Providers[1].Windows) != 2 || got.Providers[1].Windows[1].UsedPercent != 23 {
		t.Fatalf("Anthropic windows = %#v", got.Providers[1].Windows)
	}
	if body := rec.Body.String(); strings.Contains(body, "openai-secret") || strings.Contains(body, "anthropic-secret") || strings.Contains(body, "account-secret") {
		t.Fatalf("response exposed credentials: %s", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/subscription-usage", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/subscription-usage", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d", rec.Code)
	}
}

func TestSubscriptionUsageMissingAndPartialFailure(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"openai":{"type":"oauth","access":"expired","expires":1},
		"anthropic":{"type":"oauth","access":"anthropic-secret","expires":4102444800000}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "anthropic-secret must not escape", http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	got, err := fetchSubscriptionUsage(t.Context(), subscriptionUsageClient{
		http:         http.DefaultClient,
		authPath:     authPath,
		openAIURL:    upstream.URL,
		anthropicURL: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 2 || got.Providers[0].Status != "expired" || got.Providers[1].Status != "rate_limited" {
		t.Fatalf("providers = %#v", got.Providers)
	}
}

func TestSubscriptionUsageRejectsMalformedAuthFile(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openai":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := fetchSubscriptionUsage(t.Context(), subscriptionUsageClient{authPath: authPath})
	if err == nil {
		t.Fatal("expected malformed auth error")
	}

	srv := testServer(t)
	srv.subscriptionUsage.authPath = authPath
	mux, routeErr := srv.routes()
	if routeErr != nil {
		t.Fatal(routeErr)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/subscription-usage", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), authPath) {
		t.Fatalf("malformed auth response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestSubscriptionUsageMissingAuthFile(t *testing.T) {
	got, err := fetchSubscriptionUsage(t.Context(), subscriptionUsageClient{authPath: filepath.Join(t.TempDir(), "missing.json")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Providers == nil || len(got.Providers) != 0 {
		t.Fatalf("providers = %#v, want empty list", got.Providers)
	}
}

func TestSubscriptionUsageDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openai":{"type":"oauth","access":"openai-secret","expires":4102444800000,"accountId":"account-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := newSubscriptionUsageClient()
	client.authPath = authPath
	client.openAIURL = source.URL
	got, err := fetchSubscriptionUsage(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	if redirected.Load() {
		t.Fatal("usage client followed redirect")
	}
	if len(got.Providers) != 1 || got.Providers[0].Status != "upstream_error" {
		t.Fatalf("providers = %#v", got.Providers)
	}
}
