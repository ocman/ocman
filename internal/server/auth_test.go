package server

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

var testPasswordHashes sync.Map // plaintext -> []byte

// testPasswordHash returns a minimum-cost bcrypt hash, cached per password.
// Login verification reads the cost from the hash, so tests exercise the
// production comparison without paying HashPassword's cost on every fixture.
// TestHashPasswordUsesProductionCost pins the production cost.
func testPasswordHash(t testing.TB, password string) []byte {
	t.Helper()
	if hash, ok := testPasswordHashes.Load(password); ok {
		return hash.([]byte)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	actual, _ := testPasswordHashes.LoadOrStore(password, hash)
	return actual.([]byte)
}

// newTestAuth builds an Auth with the given plaintext password and a
// deterministic HMAC key so tests can exercise cookie signing without
// fighting rand. The default is strict (no localhost bypass); pass
// withTrustLocalhost to restore the dev-mode exemption.
func newTestAuth(t *testing.T, password string, opts ...testAuthOption) *Auth {
	t.Helper()
	cfg := AuthConfig{
		HMACKey:   []byte("deterministic-test-key-of-32-ch!"),
		CookieTTL: time.Hour,
	}
	cfg.PasswordHash = testPasswordHash(t, password)
	for _, opt := range opts {
		opt(&cfg)
	}
	a, err := NewAuth(cfg)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil auth")
	}
	return a
}

type testAuthOption func(*AuthConfig)

func withTrustLocalhost() testAuthOption {
	return func(c *AuthConfig) { c.TrustLocalhost = true }
}

// --- NewAuth construction ---

func TestHashPasswordUsesProductionCost(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost(hash); err != nil || cost != bcryptCost || cost < 12 {
		t.Fatalf("bcrypt cost = %d, %v; want production cost %d", cost, err, bcryptCost)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte("hunter2")) != nil {
		t.Fatal("production hash does not verify its password")
	}
}

func TestTestPasswordHashVerifiesOnlyItsPassword(t *testing.T) {
	hash := testPasswordHash(t, "hunter2")
	if bcrypt.CompareHashAndPassword(hash, []byte("hunter2")) != nil {
		t.Fatal("test hash does not verify its password")
	}
	if bcrypt.CompareHashAndPassword(hash, []byte("wrong")) == nil {
		t.Fatal("test hash accepted the wrong password")
	}
	if other := testPasswordHash(t, "other"); bcrypt.CompareHashAndPassword(other, []byte("hunter2")) == nil {
		t.Fatal("cached hashes leaked between passwords")
	}
}

func TestNewAuth_DisabledWhenNoHash(t *testing.T) {
	a, err := NewAuth(AuthConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a != nil {
		t.Errorf("expected nil auth when no password, got %+v", a)
	}
}

func TestNewAuth_ShortKeyRejected(t *testing.T) {
	hash := testPasswordHash(t, "hunter2")
	_, err := NewAuth(AuthConfig{PasswordHash: hash, HMACKey: []byte("tiny")})
	if err == nil {
		t.Error("expected error for short hmac key")
	}
}

// TestTrustsLocalhost pins the post-flip default (strict: even loopback
// must present a cookie), that the opt-in flag reaches the constructed
// Auth verbatim, and that the accessor survives the nil receiver callers
// see when auth is disabled entirely.
func TestTrustsLocalhost(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth *Auth
		want bool
	}{
		{"default", newTestAuth(t, "hunter2"), false},
		{"opt-in", newTestAuth(t, "hunter2", withTrustLocalhost()), true},
		{"nil", nil, false},
	} {
		if got := tc.auth.TrustsLocalhost(); got != tc.want {
			t.Errorf("%s: TrustsLocalhost() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNewAuth_DefaultsTTL(t *testing.T) {
	hash := testPasswordHash(t, "hunter2")
	a, err := NewAuth(AuthConfig{
		PasswordHash: hash,
		HMACKey:      []byte("deterministic-test-key-of-32-ch!"),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if a.cookieTTL != defaultCookieTTL {
		t.Errorf("ttl = %v, want default %v", a.cookieTTL, defaultCookieTTL)
	}
}

// --- cookie round-trip ---

func TestCookie_RoundTrip(t *testing.T) {
	a := newTestAuth(t, "hunter2")

	// Issue a cookie into a recorder, then replay it on a fresh
	// request and check verification.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	a.issueCookie(w, r)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName {
		t.Fatalf("expected one %s cookie, got %+v", authCookieName, cookies)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.AddCookie(cookies[0])
	if !a.hasValidCookie(r2) {
		t.Error("freshly-issued cookie should verify")
	}
}

func TestCookie_RejectsInvalidTokens(t *testing.T) {
	a := newTestAuth(t, "hunter2")
	issued := issuedCookie(a).Value
	other, err := NewAuth(AuthConfig{
		PasswordHash: testPasswordHash(t, "hunter2"),
		HMACKey:      []byte("key-two-thirty-two-bytes-long!!!"),
		CookieTTL:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"tampered":     issued[:len(issued)-1] + "X", // final signature byte flipped
		"expired":      a.signToken(time.Now().Add(-time.Hour)),
		"wrong key":    other.signToken(time.Now().Add(time.Hour)),
		"empty":        "",
		"no expiry":    ".sig",
		"no signature": "9999",
		"bad expiry":   "abc.def",
		"non-numeric":  "notanumber.sig",
	} {
		if a.verifyToken(token) {
			t.Errorf("%s token %q should not verify", name, token)
		}
	}
}

// issuedCookie returns a freshly issued auth cookie.
func issuedCookie(a *Auth) *http.Cookie {
	w := httptest.NewRecorder()
	a.issueCookie(w, httptest.NewRequest(http.MethodGet, "/", nil))
	return w.Result().Cookies()[0]
}

// authRequest builds a request from remoteAddr, with the auth cookie when
// cookie is non-nil.
func authRequest(method, target, remoteAddr, body string, cookie *http.Cookie) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

const (
	testRemoteAddr   = "10.0.0.5:1234"
	testLoopbackAddr = "127.0.0.1:12345"
)

// testAuthMode builds the Auth for a mode: "off" (auth disabled),
// "strict" (the default) or "trusted" (TrustLocalhost).
func testAuthMode(t *testing.T, mode string) *Auth {
	switch mode {
	case "off":
		return nil
	case "trusted":
		return newTestAuth(t, "hunter2", withTrustLocalhost())
	}
	return newTestAuth(t, "hunter2")
}

// --- requireAuth middleware ---

// TestRequireAuth covers the middleware: disabled auth passes through but
// still rejects cross-site writes; strict mode (the post-flip default)
// sends every anonymous client, loopback included, to the lockscreen but
// accepts a valid cookie; the opt-in TrustLocalhost lets loopback through.
func TestRequireAuth(t *testing.T) {
	for _, tc := range []struct {
		name, mode, method, target, addr, origin string
		cookie                                   bool
		want                                     int
	}{
		{name: "disabled passes through", mode: "off", addr: testRemoteAddr, want: http.StatusOK},
		{name: "disabled rejects cross-site post", mode: "off", method: http.MethodPost, target: "http://localhost:8228/api/sessions", origin: "https://evil.example", want: http.StatusForbidden},
		{name: "trusted IPv4 loopback bypasses", mode: "trusted", addr: testLoopbackAddr, want: http.StatusOK},
		{name: "trusted IPv6 loopback bypasses", mode: "trusted", addr: "[::1]:12345", want: http.StatusOK},
		{name: "strict IPv4 loopback requires auth", mode: "strict", addr: testLoopbackAddr, want: http.StatusUnauthorized},
		{name: "strict IPv6 loopback requires auth", mode: "strict", addr: "[::1]:12345", want: http.StatusUnauthorized},
		{name: "strict loopback with cookie", mode: "strict", addr: testLoopbackAddr, cookie: true, want: http.StatusOK},
		{name: "strict anonymous remote", mode: "strict", addr: testRemoteAddr, want: http.StatusUnauthorized},
		{name: "strict remote with cookie", mode: "strict", addr: testRemoteAddr, cookie: true, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testAuthMode(t, tc.mode)
			called := false
			handler := (&Server{auth: a}).requireAuth(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			method, target := cmp.Or(tc.method, http.MethodGet), cmp.Or(tc.target, "/api/stats")
			var cookie *http.Cookie
			if tc.cookie {
				cookie = issuedCookie(a)
			}
			req := authRequest(method, target, cmp.Or(tc.addr, httptest.DefaultRemoteAddr), "", cookie)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
			}
			if called != (tc.want == http.StatusOK) {
				t.Errorf("handler called = %v, want %v", called, !called)
			}
		})
	}
}

// --- /api/auth/me ---

// TestHandleAuthMe: a trusted loopback client sees itself as already
// authenticated so the frontend skips the lockscreen; in strict mode an
// anonymous loopback client is treated like any remote one.
func TestHandleAuthMe(t *testing.T) {
	for _, tc := range []struct {
		name, mode, addr         string
		wantRequired, wantAuthed bool
	}{
		{"disabled", "off", testRemoteAddr, false, true},
		{"trusted loopback", "trusted", testLoopbackAddr, true, true},
		{"untrusted loopback", "strict", testLoopbackAddr, true, false},
		{"anonymous remote", "strict", testRemoteAddr, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			(&Server{auth: testAuthMode(t, tc.mode)}).handleAuthMe(rr, authRequest(http.MethodGet, "/api/auth/me", tc.addr, "", nil))

			var got map[string]interface{}
			_ = json.Unmarshal(rr.Body.Bytes(), &got)
			if got["authRequired"] != tc.wantRequired || got["authenticated"] != tc.wantAuthed {
				t.Errorf("authRequired = %v, authenticated = %v; want %v, %v", got["authRequired"], got["authenticated"], tc.wantRequired, tc.wantAuthed)
			}
		})
	}
}

// --- /api/auth/login ---

func TestHandleAuthLogin_Rejections(t *testing.T) {
	for _, tc := range []struct {
		name, mode, password string
		want                 int
	}{
		{"disabled", "off", "whatever", http.StatusNoContent},
		{"wrong password", "strict", "wrong", http.StatusUnauthorized},
		{"empty password", "strict", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			(&Server{auth: testAuthMode(t, tc.mode)}).handleAuthLogin(rr, authRequest(http.MethodPost, "/api/auth/login", testRemoteAddr, `{"password":"`+tc.password+`"}`, nil))
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
			}
			if len(rr.Result().Cookies()) != 0 {
				t.Error("no cookie should be set on a rejected login")
			}
		})
	}
}

func TestHandleAuthLogin_Success_IssuesCookie(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName {
		t.Fatalf("expected %s cookie, got %+v", authCookieName, cookies)
	}
	if !cookies[0].HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookies[0].SameSite)
	}
}

// TestHandleAuthLogin_RateLimited: after loginMaxAttempts failures the next
// attempt is rate-limited regardless of password. Strict mode treats
// localhost like every other IP, so a runaway local process can't
// brute-force without backoff.
func TestHandleAuthLogin_RateLimited(t *testing.T) {
	for _, tc := range []struct{ addr, password string }{
		{testRemoteAddr, "hunter2"},
		{testLoopbackAddr, "wrong"},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			srv := &Server{auth: newTestAuth(t, "hunter2")}
			for i := 0; i < loginMaxAttempts; i++ {
				rr := httptest.NewRecorder()
				srv.handleAuthLogin(rr, authRequest(http.MethodPost, "/api/auth/login", tc.addr, `{"password":"wrong"}`, nil))
				if rr.Code != http.StatusUnauthorized {
					t.Fatalf("attempt %d: got %d, want 401", i, rr.Code)
				}
			}
			rr := httptest.NewRecorder()
			srv.handleAuthLogin(rr, authRequest(http.MethodPost, "/api/auth/login", tc.addr, `{"password":"`+tc.password+`"}`, nil))
			if rr.Code != http.StatusTooManyRequests {
				t.Errorf("expected 429 after %d failures, got %d", loginMaxAttempts, rr.Code)
			}
		})
	}
}

// TestHandleAuthLogin_LoopbackSkipsRateLimit_WhenTrusted keeps the
// dev-mode property: with TrustLocalhost set, a local user can hammer
// the login endpoint without locking themselves out.
func TestHandleAuthLogin_LoopbackSkipsRateLimit_WhenTrusted(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2", withTrustLocalhost())}
	for i := 0; i < loginMaxAttempts+5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		srv.handleAuthLogin(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("trusted loopback should never be rate-limited (attempt %d)", i)
		}
	}
}

func TestHandleAuthLogin_SuccessResetsLimiter(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	// A few failures, then a success, then another failure — counter
	// must have been reset.
	for i := 0; i < loginMaxAttempts-1; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "10.0.0.5:1234"
		srv.handleAuthLogin(httptest.NewRecorder(), req)
	}
	// Correct password.
	okReq := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	okReq.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, okReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("success: got %d", rr.Code)
	}
	// Fresh failure after reset should be a plain 401, not 429.
	failReq := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"password":"wrong"}`))
	failReq.RemoteAddr = "10.0.0.5:1234"
	rr = httptest.NewRecorder()
	srv.handleAuthLogin(rr, failReq)
	if rr.Code == http.StatusTooManyRequests {
		t.Error("limiter should have been reset after successful login")
	}
}

// --- /api/auth/logout ---

func TestHandleAuthLogout_ClearsCookie(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogout(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", cookies[0].MaxAge)
	}
	if cookies[0].Value != "" {
		t.Errorf("Value = %q, want empty", cookies[0].Value)
	}
}

func TestHandleAuthLogout_Disabled204(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	rr := httptest.NewRecorder()
	srv.handleAuthLogout(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
}

// --- loginLimiter ---

// TestLoginLimiter_WindowExpiry verifies that once the fixed window has
// elapsed (windowStart is older than loginWindow), a new request resets
// the bucket and is allowed — even if the previous window was exhausted.
func TestLoginLimiter_WindowExpiry(t *testing.T) {
	var l loginLimiter

	const ip = "10.0.0.42"

	// Fill the window to exhaustion.
	for i := 0; i < loginMaxAttempts; i++ {
		if !l.allow(ip) {
			t.Fatalf("attempt %d should be allowed inside the window", i)
		}
	}
	// One more should be blocked.
	if l.allow(ip) {
		t.Fatal("window should be exhausted; expected allow=false")
	}

	// Backdate the window start so it looks expired.
	l.mu.Lock()
	l.buckets[ip].windowStart = l.buckets[ip].windowStart.Add(-(loginWindow + time.Second))
	l.mu.Unlock()

	// After the window expires, the next call must reset the bucket and allow.
	if !l.allow(ip) {
		t.Error("expired window: expected allow=true after window reset")
	}

	// The attempt counter must have been reset to 1 (just this call).
	l.mu.Lock()
	attempts := l.buckets[ip].attempts
	l.mu.Unlock()
	if attempts != 1 {
		t.Errorf("attempts after window reset = %d, want 1", attempts)
	}
}

// TestLoginLimiter_EmptyIPAlwaysDenied guards that an unknown/empty IP is
// never allowed through the rate limiter — it should be treated as blocked,
// not as a free pass that bypasses rate limiting entirely.
func TestLoginLimiter_EmptyIPAlwaysDenied(t *testing.T) {
	var l loginLimiter
	for i := 0; i < loginMaxAttempts+10; i++ {
		if l.allow("") {
			t.Fatalf("empty IP must always be denied (attempt %d)", i)
		}
	}
}

// TestLoginLimiter_ResetClearsBucket ensures reset() removes the entry
// so the next call starts a fresh window.
func TestLoginLimiter_ResetClearsBucket(t *testing.T) {
	var l loginLimiter
	const ip = "192.168.1.1"

	// Exhaust the window.
	for i := 0; i < loginMaxAttempts; i++ {
		l.allow(ip)
	}
	if l.allow(ip) {
		t.Fatal("expected blocked after exhaustion")
	}

	l.reset(ip)

	// After reset the first attempt should be allowed.
	if !l.allow(ip) {
		t.Error("expected allow=true after reset")
	}
}

// TestLoginLimiter_GCSweepsExpiredEntries verifies that once the bucket
// map crosses the GC threshold, expired entries are dropped on the next
// allow() call. This prevents the map from growing unbounded when a
// scanner or IPv6-rotating client probes from many distinct addresses.
func TestLoginLimiter_GCSweepsExpiredEntries(t *testing.T) {
	var l loginLimiter

	// Seed the map past the threshold with stale entries (windowStart
	// well beyond 2*loginWindow ago — older than any live request
	// could be).
	const stale = 1500
	l.mu.Lock()
	l.buckets = make(map[string]*loginBucket, stale)
	staleStart := time.Now().Add(-(3 * loginWindow))
	for i := 0; i < stale; i++ {
		l.buckets[fmt.Sprintf("10.%d.%d.%d", i%256, (i/256)%256, (i/65536)%256)] = &loginBucket{
			windowStart: staleStart,
			attempts:    1,
		}
	}
	l.mu.Unlock()

	// Any subsequent allow() should trigger the sweep and leave just
	// the fresh entry behind.
	if !l.allow("203.0.113.1") {
		t.Fatal("fresh IP should be allowed")
	}

	l.mu.Lock()
	remaining := len(l.buckets)
	_, kept := l.buckets["203.0.113.1"]
	l.mu.Unlock()

	if remaining != 1 {
		t.Errorf("after GC: bucket count = %d, want 1", remaining)
	}
	if !kept {
		t.Error("after GC: the fresh bucket was unexpectedly removed")
	}
}

// TestLoginLimiter_GCKeepsActiveEntries verifies that the sweep doesn't
// evict buckets that are still inside the live window — even when the
// map is over the GC threshold.
func TestLoginLimiter_GCKeepsActiveEntries(t *testing.T) {
	var l loginLimiter

	// Seed past the threshold, half stale and half fresh.
	const total = 1100
	l.mu.Lock()
	l.buckets = make(map[string]*loginBucket, total)
	now := time.Now()
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("198.51.%d.%d", i/256, i%256)
		ws := now
		if i%2 == 0 {
			ws = now.Add(-(3 * loginWindow))
		}
		l.buckets[key] = &loginBucket{windowStart: ws, attempts: 1}
	}
	l.mu.Unlock()

	if !l.allow("203.0.113.2") {
		t.Fatal("fresh IP should be allowed")
	}

	l.mu.Lock()
	remaining := len(l.buckets)
	l.mu.Unlock()

	// Half were stale (total/2 = 550) and got swept; the other half
	// stay, plus the new entry.
	wantMin := total / 2
	if remaining < wantMin || remaining > wantMin+5 {
		t.Errorf("after GC: bucket count = %d, want ~%d", remaining, wantMin+1)
	}
}

// --- clientIP ---

func TestClientIP(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"127.0.0.1:12345", "127.0.0.1"},
		{"[::1]:8080", "::1"},
		{"192.168.1.1:443", "192.168.1.1"},
		{"no-port", "no-port"}, // fallback
	}
	for _, tt := range tests {
		r := &http.Request{RemoteAddr: tt.remote}
		if got := clientIP(r); got != tt.want {
			t.Errorf("clientIP(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

// TestCookieSecureFollowsPublicBaseURL covers the TLS-terminating-proxy
// deployment: r.TLS is nil on the hop from the proxy, so deriving Secure
// from it alone ships a 30-day auth cookie over a scheme the browser may
// downgrade. The configured public base URL is trusted config (unlike
// X-Forwarded-Proto), so an https:// one marks the cookie Secure.
func TestCookieSecureFollowsPublicBaseURL(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		wantSecure bool
	}{
		{"https public base URL", "https://ocman.example.com", true},
		{"https base URL with trailing slash", "https://ocman.example.com/", true},
		{"http public base URL", "http://ocman.example.com", false},
		{"no public base URL", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(nil, nil, "127.0.0.1:0", platforms.NewRegistry(), newTestAuth(t, "hunter2")).
				WithPublicBaseURL(tt.baseURL)

			w := httptest.NewRecorder()
			// Plain HTTP request: exactly what a reverse proxy forwards.
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"hunter2"}`))
			srv.handleAuthLogin(w, r)

			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected one cookie, got %+v (status %d)", cookies, w.Code)
			}
			if cookies[0].Secure != tt.wantSecure {
				t.Errorf("cookie Secure = %v, want %v", cookies[0].Secure, tt.wantSecure)
			}

			// Logout's clearing cookie must match, or the browser keeps
			// the Secure one alongside a non-Secure empty duplicate.
			lw := httptest.NewRecorder()
			srv.handleAuthLogout(lw, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))
			cleared := lw.Result().Cookies()
			if len(cleared) != 1 {
				t.Fatalf("expected one clearing cookie, got %+v", cleared)
			}
			if cleared[0].Secure != tt.wantSecure {
				t.Errorf("clearing cookie Secure = %v, want %v", cleared[0].Secure, tt.wantSecure)
			}
		})
	}
}
