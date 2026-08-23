package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientActivityPolicyDemandExpiresAndRequiresVisible(t *testing.T) {
	now := time.Unix(100, 0)
	p := newClientActivityPolicy(func() time.Time { return now })

	if err := p.Update(clientActivityLease{
		ClientID: "client-1", Visible: true, Focused: true, RecentlyInteracted: true,
		Scopes: []string{"sessions", "session:abc", "git-status:/tmp/repo"}, TTLMS: 45_000,
	}); err != nil {
		t.Fatal(err)
	}
	if !p.HasDemand("sessions") || !p.HasDemand("session:abc") {
		t.Fatal("visible unexpired lease should create demand")
	}

	if err := p.Update(clientActivityLease{ClientID: "client-1", Visible: false, Scopes: []string{"sessions"}, TTLMS: 45_000}); err != nil {
		t.Fatal(err)
	}
	if p.HasDemand("sessions") {
		t.Fatal("hidden lease must not create demand")
	}

	if err := p.Update(clientActivityLease{ClientID: "client-1", Visible: true, Scopes: []string{"sessions"}, TTLMS: 45_000}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(45*time.Second + time.Millisecond)
	if p.HasDemand("sessions") {
		t.Fatal("expired lease must not create demand")
	}
}

func TestClientActivityPolicyValidatesAndBoundsInput(t *testing.T) {
	now := time.Unix(100, 0)
	p := newClientActivityPolicy(func() time.Time { return now })

	for _, tc := range []struct {
		name  string
		lease clientActivityLease
	}{
		{name: "empty client id", lease: clientActivityLease{Visible: true, Scopes: []string{"sessions"}, TTLMS: 45_000}},
		{name: "malformed client id", lease: clientActivityLease{ClientID: "bad id", Visible: true, Scopes: []string{"sessions"}, TTLMS: 45_000}},
		{name: "unknown scope", lease: clientActivityLease{ClientID: "client", Visible: true, Scopes: []string{"settings"}, TTLMS: 45_000}},
		{name: "empty prefixed scope", lease: clientActivityLease{ClientID: "client", Visible: true, Scopes: []string{"session:"}, TTLMS: 45_000}},
		{name: "too many scopes", lease: clientActivityLease{ClientID: "client", Visible: true, Scopes: make([]string, maxClientActivityScopes+1), TTLMS: 45_000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.Update(tc.lease); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	for i := 0; i < maxClientActivityClients; i++ {
		if err := p.Update(clientActivityLease{ClientID: "client-" + strings.Repeat("x", i/10) + string(rune('0'+i%10)), Scopes: []string{"projects"}, TTLMS: 45_000}); err != nil {
			t.Fatalf("lease %d: %v", i, err)
		}
	}
	if err := p.Update(clientActivityLease{ClientID: "one-too-many", Scopes: []string{"projects"}, TTLMS: 45_000}); err == nil {
		t.Fatal("expected client bound error")
	}
	now = now.Add(maxClientActivityTTL + time.Millisecond)
	if err := p.Update(clientActivityLease{ClientID: "after-expiry", Scopes: []string{"projects"}, TTLMS: 45_000}); err != nil {
		t.Fatalf("lazy expiration should free client slots: %v", err)
	}
}

func TestClientActivityPolicyClampsTTL(t *testing.T) {
	now := time.Unix(100, 0)
	p := newClientActivityPolicy(func() time.Time { return now })
	if err := p.Update(clientActivityLease{ClientID: "short", Visible: true, Scopes: []string{"metrics"}, TTLMS: 1}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(minClientActivityTTL - time.Millisecond)
	if !p.HasDemand("metrics") {
		t.Fatal("TTL should be clamped to the minimum")
	}

	now = time.Unix(200, 0)
	if err := p.Update(clientActivityLease{ClientID: "long", Visible: true, Scopes: []string{"workflows"}, TTLMS: int64(^uint64(0) >> 1)}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(minClientActivityTTL + time.Millisecond)
	if !p.HasDemand("workflows") {
		t.Fatal("overflowing TTL should be clamped to the maximum, not the minimum")
	}
	now = time.Unix(200, 0).Add(maxClientActivityTTL + time.Millisecond)
	if p.HasDemand("workflows") {
		t.Fatal("TTL should be clamped to the maximum")
	}
}

func TestClientActivityEndpointRequiresAuthAndRecordsLease(t *testing.T) {
	auth := newTestAuth(t, "hunter2")
	srv := New(nil, nil, "", nil, auth)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"clientId":"client-1","visible":true,"focused":true,"recentlyInteracted":true,"scopes":["sessions"],"ttlMs":45000}`)

	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/client-activity", bytes.NewReader(body))
	unauthenticated.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, unauthenticated)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rr.Code)
	}

	cookieWriter := httptest.NewRecorder()
	auth.issueCookie(cookieWriter, httptest.NewRequest(http.MethodGet, "/", nil))
	request := httptest.NewRequest(http.MethodPost, "/api/client-activity", bytes.NewReader(body))
	request.RemoteAddr = "10.0.0.5:1234"
	request.AddCookie(cookieWriter.Result().Cookies()[0])
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d, want 204: %s", rr.Code, rr.Body.String())
	}
	if !srv.activity.HasDemand("sessions") {
		t.Fatal("endpoint did not record sessions demand")
	}
}
