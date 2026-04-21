package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// Auth protects clients behind a password lockscreen.
//
// Design:
//   - Auth is only engaged when a password is configured. If nil, every
//     request is allowed through and ocman behaves exactly like the
//     pre-auth versions.
//   - By default every client must authenticate, including localhost.
//     Set TrustLocalhost to restore the traditional "local user is
//     trusted" escape hatch — handy for dev loops where a password
//     prompt on every `air` restart would be tedious.
//   - Sessions are stateless signed cookies. The HMAC key is persisted
//     in state.db so cookies survive restarts up to their TTL; rotating
//     the key invalidates every cookie at once.
//   - Login attempts are rate-limited per remote IP to make online
//     brute-force unattractive. The window is short enough not to
//     lock out a user who fat-fingered their password a few times.
//     Localhost clients skip the limiter only when TrustLocalhost is
//     set; otherwise they're rate-limited like anyone else so a
//     malicious local process can't brute-force without backoff.
type Auth struct {
	passwordHash   []byte
	hmacKey        []byte
	cookieTTL      time.Duration
	cookieName     string
	trustLocalhost bool

	// limiter gates /api/auth/login attempts per source IP. Zero
	// value is usable.
	limiter loginLimiter
}

// AuthConfig captures everything needed to construct an Auth. The
// password is pre-hashed by the caller (main.go) so the plaintext
// never crosses the package boundary.
type AuthConfig struct {
	PasswordHash []byte        // bcrypt hash; required
	HMACKey      []byte        // 32+ random bytes; required
	CookieTTL    time.Duration // e.g. 30 days; zero picks the default
	// TrustLocalhost, when true, exempts loopback clients from auth.
	// Default (false) means every client must present a valid cookie.
	TrustLocalhost bool
}

// TrustsLocalhost reports whether localhost clients bypass auth.
// Exposed for diagnostics (e.g. the boot log); middleware paths
// consult the internal field directly.
func (a *Auth) TrustsLocalhost() bool {
	if a == nil {
		return false
	}
	return a.trustLocalhost
}

// Default cookie parameters.
const (
	authCookieName    = "ocman_auth"
	defaultCookieTTL  = 30 * 24 * time.Hour
	bcryptCost        = 12
	hmacKeySize       = 32
	maxLoginBodyBytes = 4 * 1024

	// loginMaxAttempts is the number of failed attempts an IP may
	// make inside loginWindow before being told to slow down.
	loginMaxAttempts = 5
	loginWindow      = time.Minute
)

// NewAuth returns an Auth configured with the supplied hash + key, or
// nil if cfg.PasswordHash is empty (auth disabled).
func NewAuth(cfg AuthConfig) (*Auth, error) {
	if len(cfg.PasswordHash) == 0 {
		return nil, nil
	}
	if len(cfg.HMACKey) < 16 {
		return nil, fmt.Errorf("hmac key too short: got %d bytes, want >= 16", len(cfg.HMACKey))
	}
	ttl := cfg.CookieTTL
	if ttl <= 0 {
		ttl = defaultCookieTTL
	}
	return &Auth{
		passwordHash:   append([]byte(nil), cfg.PasswordHash...),
		hmacKey:        append([]byte(nil), cfg.HMACKey...),
		cookieTTL:      ttl,
		cookieName:     authCookieName,
		trustLocalhost: cfg.TrustLocalhost,
	}, nil
}

// HashPassword returns a bcrypt hash suitable for AuthConfig.PasswordHash.
// Exposed at package level so main.go can hash the plaintext once at
// startup and discard it.
func HashPassword(plaintext string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
}

// GenerateHMACKey returns a cryptographically random key of the size
// used by the cookie signer.
func GenerateHMACKey() ([]byte, error) {
	buf := make([]byte, hmacKeySize)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generating hmac key: %w", err)
	}
	return buf, nil
}

// --- cookie signing ---

// signToken produces a value of the form "<expires_unix>.<base64-sig>".
// It's opaque to the client, but trivial to verify without any
// server-side session store.
func (a *Auth) signToken(expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, a.hmacKey)
	mac.Write([]byte(exp))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return exp + "." + sig
}

// verifyToken returns true if the token was signed with the current
// key and has not yet expired.
func (a *Auth) verifyToken(token string) bool {
	idx := strings.IndexByte(token, '.')
	if idx <= 0 || idx == len(token)-1 {
		return false
	}
	expStr, sig := token[:idx], token[idx+1:]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() >= exp {
		return false
	}
	mac := hmac.New(sha256.New, a.hmacKey)
	mac.Write([]byte(expStr))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	// constant-time comparison to avoid leaking timing info
	return subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) == 1
}

// hasValidCookie returns true when the request carries a live cookie.
func (a *Auth) hasValidCookie(r *http.Request) bool {
	c, err := r.Cookie(a.cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return a.verifyToken(c.Value)
}

// issueCookie sets a fresh auth cookie on the response.
func (a *Auth) issueCookie(w http.ResponseWriter, r *http.Request) {
	expires := time.Now().Add(a.cookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    a.signToken(expires),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(a.cookieTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure(r),
	})
}

// clearCookie instructs the browser to drop the auth cookie.
func (a *Auth) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure(r),
	})
}

// isSecure returns true if the request arrived over TLS. We don't
// trust X-Forwarded-Proto by default — ocman doesn't know whether a
// proxy sits in front of it, and taking the header at face value
// would let a local attacker downgrade the cookie.
func isSecure(r *http.Request) bool {
	return r.TLS != nil
}

// --- middleware ---

// requireAuth wraps a handler so clients must present a valid auth
// cookie. When auth is disabled (s.auth == nil) the wrapper is a
// pass-through. When auth.trustLocalhost is true, loopback clients
// bypass the check (the dev-mode escape hatch); otherwise even
// localhost must authenticate.
//
// The middleware deliberately doesn't authenticate static asset
// requests: the SPA and its lockscreen must be reachable even for an
// unauthenticated client. Only API routes return 401.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			next(w, r)
			return
		}
		if s.auth.trustLocalhost && isLoopback(r) {
			next(w, r)
			return
		}
		if s.auth.hasValidCookie(r) {
			next(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// --- handlers ---

// handleAuthLogin verifies the password and sets the cookie. It's
// rate-limited per source IP. When trustLocalhost is set, loopback
// requests skip the limiter (can't lock yourself out of your own
// dev machine); otherwise everyone is on the same clock.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		// Auth is disabled — treating login as a no-op with a 204
		// keeps the API shape consistent for clients that probe
		// /api/auth/me and then always POST /api/auth/login.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ip := clientIP(r)
	skipLimiter := s.auth.trustLocalhost && isLoopback(r)
	if !skipLimiter && !s.auth.limiter.allow(ip) {
		http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if !readAndUnmarshal(w, r, maxLoginBodyBytes, &req) {
		return
	}
	if req.Password == "" {
		http.Error(w, "password required", http.StatusBadRequest)
		return
	}

	if err := bcrypt.CompareHashAndPassword(s.auth.passwordHash, []byte(req.Password)); err != nil {
		log.WithField("ip", ip).Warn("auth: failed login attempt")
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}

	// Successful login: reset the limiter for this IP so a user who
	// finally got their password right isn't stuck behind 429s.
	s.auth.limiter.reset(ip)
	s.auth.issueCookie(w, r)
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleAuthLogout clears the cookie. Always returns 204; idempotent.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		s.auth.clearCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthMe reports whether auth is configured and whether the
// current request is authenticated. The frontend calls this on boot
// to decide whether to show the lockscreen.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"authRequired":  s.auth != nil,
		"authenticated": true,
	}
	if s.auth == nil {
		writeJSON(w, resp)
		return
	}
	// Loopback bypass: only when the operator has explicitly opted in.
	if s.auth.trustLocalhost && isLoopback(r) {
		writeJSON(w, resp)
		return
	}
	resp["authenticated"] = s.auth.hasValidCookie(r)
	writeJSON(w, resp)
}

// --- rate limiter ---

// loginLimiter is a tiny fixed-window limiter keyed by IP. It's
// intentionally naive (no sliding window, no per-user buckets) because
// ocman is single-user and the threat is casual brute-force, not a
// resourceful attacker.
type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
}

type loginBucket struct {
	windowStart time.Time
	attempts    int
}

func (l *loginLimiter) allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = make(map[string]*loginBucket)
	}

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.Sub(b.windowStart) > loginWindow {
		l.buckets[ip] = &loginBucket{windowStart: now, attempts: 1}
		return true
	}
	b.attempts++
	return b.attempts <= loginMaxAttempts
}

func (l *loginLimiter) reset(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, ip)
}

// clientIP returns the source IP of the request without the port.
// Falls back to the raw RemoteAddr if splitting fails.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
