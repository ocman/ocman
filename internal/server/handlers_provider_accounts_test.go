package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type accountPlatform struct {
	*fakePlatform
	account *providerprotocol.Account
	err     error
}

func (p accountPlatform) Account(context.Context) (*providerprotocol.Account, error) {
	return p.account, p.err
}

func TestProviderAccountsListsAccountPlatforms(t *testing.T) {
	srv := testServer(t)
	srv.registry.Register(accountPlatform{fakePlatform: &fakePlatform{id: "claudecode", displayName: "Claude Code"}, account: &providerprotocol.Account{
		SubscriptionType: "max",
		RateLimits:       []providerprotocol.RateLimit{{Name: "five_hour", Utilization: 0.28, ResetsAt: 1}, {Name: "weekly", Utilization: 0.76, ResetsAt: 2}},
	}})
	srv.registry.Register(accountPlatform{fakePlatform: &fakePlatform{id: "p-out"}, err: platforms.ErrNotFound})
	srv.registry.Register(accountPlatform{fakePlatform: &fakePlatform{id: "p-down"}, err: errors.New("dial")})
	srv.registry.Register(accountPlatform{fakePlatform: &fakePlatform{id: "p-none"}, err: platforms.ErrUnsupported})
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/provider-accounts", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var got struct {
		Accounts []providerAccount `json:"accounts"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(got.Accounts) != 3 {
		t.Fatalf("accounts = %+v", got.Accounts)
	}
	claude, down, out := got.Accounts[0], got.Accounts[1], got.Accounts[2]
	if claude.Platform != "claudecode" || claude.DisplayName != "Claude Code" || claude.Status != "ok" || claude.SubscriptionType != "max" ||
		len(claude.RateLimits) != 2 || claude.RateLimits[1].Name != "weekly" || claude.RateLimits[1].Utilization != 0.76 {
		t.Fatalf("claude = %+v", claude)
	}
	if down.Status != "unavailable" || out.Status != "signed_out" || out.RateLimits == nil {
		t.Fatalf("down = %+v, out = %+v", down, out)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/provider-accounts", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d", rec.Code)
	}
}
