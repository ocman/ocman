package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestAuth builds an Auth with the given plaintext password and a
// deterministic HMAC key so tests can exercise cookie signing without
// fighting rand.
func newTestAuth(t *testing.T, password string) *Auth {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	a, err := NewAuth(AuthConfig{
		PasswordHash: hash,
		HMACKey:      []byte("deterministic-test-key-of-32-ch!"),
		CookieTTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil auth")
	}
	return a
}

// --- NewAuth construction ---

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
	hash, _ := HashPassword("hunter2")
	_, err := NewAuth(AuthConfig{PasswordHash: hash, HMACKey: []byte("tiny")})
	if err == nil {
		t.Error("expected error for short hmac key")
	}
}

func TestNewAuth_DefaultsTTL(t *testing.T) {
	hash, _ := HashPassword("hunter2")
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
	r := httptest.NewRequest("GET", "/", nil)
	a.issueCookie(w, r)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName {
		t.Fatalf("expected one %s cookie, got %+v", authCookieName, cookies)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cookies[0])
	if !a.hasValidCookie(r2) {
		t.Error("freshly-issued cookie should verify")
	}
}

func TestCookie_RejectsTampering(t *testing.T) {
	a := newTestAuth(t, "hunter2")
	w := httptest.NewRecorder()
	a.issueCookie(w, httptest.NewRequest("GET", "/", nil))
	c := w.Result().Cookies()[0]

	// Flip the final byte of the signature.
	tampered := &http.Cookie{Name: c.Name, Value: c.Value[:len(c.Value)-1] + "X"}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(tampered)
	if a.hasValidCookie(r) {
		t.Error("tampered cookie should not verify")
	}
}

func TestCookie_RejectsExpired(t *testing.T) {
	a := newTestAuth(t, "hunter2")
	// Mint a token that expired an hour ago.
	past := time.Now().Add(-time.Hour)
	token := a.signToken(past)
	if a.verifyToken(token) {
		t.Error("expired token should not verify")
	}
}

func TestCookie_RejectsMalformed(t *testing.T) {
	a := newTestAuth(t, "hunter2")
	for _, bad := range []string{"", ".sig", "9999", "abc.def", "notanumber.sig"} {
		if a.verifyToken(bad) {
			t.Errorf("malformed token %q should not verify", bad)
		}
	}
}

func TestCookie_RejectsWrongKey(t *testing.T) {
	hash, _ := HashPassword("hunter2")
	a1, _ := NewAuth(AuthConfig{
		PasswordHash: hash,
		HMACKey:      []byte("key-one-thirty-two-bytes-long!!!"),
		CookieTTL:    time.Hour,
	})
	a2, _ := NewAuth(AuthConfig{
		PasswordHash: hash,
		HMACKey:      []byte("key-two-thirty-two-bytes-long!!!"),
		CookieTTL:    time.Hour,
	})

	token := a1.signToken(time.Now().Add(time.Hour))
	if a2.verifyToken(token) {
		t.Error("token signed with a1's key should not verify under a2")
	}
}

// --- requireAuth middleware ---

func TestRequireAuth_PassthroughWhenDisabled(t *testing.T) {
	srv := &Server{} // auth == nil
	called := false
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/api/stats", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	handler(rr, req)

	if !called {
		t.Error("handler should be called when auth disabled")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestRequireAuth_LoopbackBypasses(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, addr := range []string{"127.0.0.1:12345", "[::1]:12345"} {
		req := httptest.NewRequest("GET", "/api/stats", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", addr, rr.Code)
		}
	}
}

func TestRequireAuth_RejectsUnauthenticatedRemote(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("GET", "/api/stats", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestRequireAuth_AcceptsValidCookieFromRemote(t *testing.T) {
	a := newTestAuth(t, "hunter2")
	srv := &Server{auth: a}
	called := false
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Mint a valid cookie.
	w := httptest.NewRecorder()
	a.issueCookie(w, httptest.NewRequest("GET", "/", nil))
	cookie := w.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/api/stats", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if !called {
		t.Error("handler should be called for authenticated remote")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// --- /api/auth/me ---

func TestHandleAuthMe_Disabled(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthMe(rr, req)

	var got map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["authRequired"] != false {
		t.Errorf("authRequired = %v, want false", got["authRequired"])
	}
	if got["authenticated"] != true {
		t.Errorf("authenticated = %v, want true", got["authenticated"])
	}
}

func TestHandleAuthMe_EnabledLoopback(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	srv.handleAuthMe(rr, req)

	var got map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["authRequired"] != true {
		t.Errorf("authRequired = %v, want true", got["authRequired"])
	}
	// Localhost is always considered authenticated from the client's POV.
	if got["authenticated"] != true {
		t.Errorf("authenticated = %v, want true (loopback bypass)", got["authenticated"])
	}
}

func TestHandleAuthMe_EnabledRemoteAnonymous(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthMe(rr, req)

	var got map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["authRequired"] != true {
		t.Errorf("authRequired = %v, want true", got["authRequired"])
	}
	if got["authenticated"] != false {
		t.Errorf("authenticated = %v, want false", got["authenticated"])
	}
}

// --- /api/auth/login ---

func TestHandleAuthLogin_Disabled204(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"whatever"}`))
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
}

func TestHandleAuthLogin_Success_IssuesCookie(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest("POST", "/api/auth/login",
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

func TestHandleAuthLogin_WrongPassword401(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"wrong"}`))
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Error("no cookie should be set on failed login")
	}
}

func TestHandleAuthLogin_EmptyPassword400(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	req := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":""}`))
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleAuthLogin_RateLimited(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	// Fire loginMaxAttempts failures from a non-localhost IP.
	for i := 0; i < loginMaxAttempts; i++ {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "10.0.0.5:1234"
		rr := httptest.NewRecorder()
		srv.handleAuthLogin(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i, rr.Code)
		}
	}
	// Next attempt should be rate-limited regardless of password.
	req := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 after %d failures, got %d", loginMaxAttempts, rr.Code)
	}
}

func TestHandleAuthLogin_LoopbackSkipsRateLimit(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	for i := 0; i < loginMaxAttempts+5; i++ {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		srv.handleAuthLogin(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("loopback should never be rate-limited (attempt %d)", i)
		}
	}
}

func TestHandleAuthLogin_SuccessResetsLimiter(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	// A few failures, then a success, then another failure — counter
	// must have been reset.
	for i := 0; i < loginMaxAttempts-1; i++ {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "10.0.0.5:1234"
		srv.handleAuthLogin(httptest.NewRecorder(), req)
	}
	// Correct password.
	okReq := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	okReq.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	srv.handleAuthLogin(rr, okReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("success: got %d", rr.Code)
	}
	// Fresh failure after reset should be a plain 401, not 429.
	failReq := httptest.NewRequest("POST", "/api/auth/login",
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
	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
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
	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	rr := httptest.NewRecorder()
	srv.handleAuthLogout(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
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
