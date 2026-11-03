package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func TestAccountReadsOpenCodeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/experimental/usage/limits" {
			t.Errorf("path=%s", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "opencode" || password != "service-secret" {
			t.Error("missing service auth")
		}
		_, _ = w.Write([]byte(`{"data":{"workspace":{"id":"private-workspace","usageURL":"https://example.com"},"go":{"rolling":{"percent":12,"resetsAt":1793602742000},"weekly":{"percent":34},"monthly":{"percent":56}},"budgets":[{"id":"Monthly budget","limit":100,"spent":25,"resetsAt":1793602742000},{"id":"Exceeded","limit":10,"spent":20}]}}`))
	}))
	defer server.Close()
	a := New(nil, nil, nil, ocapi.New("service-secret"))
	a.rt.SetMachineServer(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	t.Cleanup(func() { a.rt.SetMachineServer("") })
	got, err := a.Account(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.SubscriptionType != "OpenCode Go" || len(got.RateLimits) != 3 || got.RateLimits[0].Utilization != .12 || got.RateLimits[0].ResetsAt != 1793602742000 {
		t.Fatalf("account=%+v", got)
	}
	if len(got.Budgets) != 2 || got.Budgets[0].RemainingUSD != 75 || got.Budgets[0].ResetsAt != "2026-11-02T06:59:02Z" || got.Budgets[1].RemainingUSD != 0 || got.Budgets[1].ResetsAt != "" {
		t.Fatalf("budgets=%+v", got.Budgets)
	}
}

func TestAccountUsageUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"signed out", 200, `{"data":null}`, platforms.ErrNotFound},
		{"unsupported", 404, ``, platforms.ErrUnsupported},
		{"not implemented", 501, ``, platforms.ErrUnsupported},
		{"unauthenticated", 401, `service-secret`, platforms.ErrPlatformUnauthenticated},
		{"forbidden", 403, `service-secret`, platforms.ErrPlatformUnauthenticated},
		{"upstream", 503, `service-secret`, nil},
		{"malformed", 200, `{`, nil},
		{"redirect", 302, ``, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 302 {
					w.Header().Set("Location", "/must-not-follow")
				}
				if r.URL.Path != "/api/experimental/usage/limits" {
					t.Error("followed redirect")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			a := New(nil, nil, nil, ocapi.Auth{})
			a.rt.SetMachineServer(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
			t.Cleanup(func() { a.rt.SetMachineServer("") })
			_, err := a.Account(t.Context())
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || strings.Contains(err.Error(), "service-secret") {
				t.Fatalf("error=%v, want=%v", err, tc.want)
			}
		})
	}
}

func TestAccountRequiresOpenCodeService(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	if _, err := a.Account(t.Context()); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("no service=%v", err)
	}
	a.rt.SetMachineServer("invalid\nport")
	if _, err := a.Account(t.Context()); err == nil {
		t.Fatal("invalid service port accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a.rt.SetMachineServer("1")
	t.Cleanup(func() { a.rt.SetMachineServer("") })
	if _, err := a.Account(ctx); err == nil {
		t.Fatal("canceled request succeeded")
	}
}

func TestConsoleUsageWithoutGo(t *testing.T) {
	got := (&consoleUsage{}).account()
	if got.SubscriptionType != "OpenCode Zen" || len(got.RateLimits) != 0 {
		t.Fatalf("account=%+v", got)
	}
}

func TestConsoleBudgetLabels(t *testing.T) {
	for _, tc := range []struct{ body, name, description string }{
		{`{"coverage":"all"}`, "Personal monthly budget", "All usage"},
		{`{"scope":"combined","coverage":"all"}`, "Group monthly budget", "All usage"},
		{`{"shared":true,"coverage":"all"}`, "Workspace monthly budget", "All usage"},
		{`{"coverage":"models","vendors":["Anthropic"],"per":"model"}`, "Personal monthly budget", "Anthropic. Per-model limit; usage shown for the most-used model."},
		{`{"coverage":"models","models":[{"name":"Sonnet"},{"name":"Opus"}]}`, "Personal monthly budget", "Sonnet, Opus"},
		{`{"coverage":"models","inputPriceAtLeast":3}`, "Personal monthly budget", "Selected models · input price at least $3 per million tokens"},
	} {
		var budget consoleBudget
		if err := json.Unmarshal([]byte(tc.body), &budget); err != nil {
			t.Fatal(err)
		}
		name, description := budget.label()
		if name != tc.name || description != tc.description {
			t.Fatalf("label=%q %q", name, description)
		}
	}
}
