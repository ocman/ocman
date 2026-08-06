package server

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// slowRequestThreshold is the wall-clock duration above which a request
// is logged at INFO instead of DEBUG. The intent is "show in normal
// production logs without manual filtering" — anything elevated to
// INFO is worth an operator's attention. Set above the p99 of healthy
// session-list builds (which can spend ~2-3s in db_get_sessions on
// large OpenCode databases) so the steady-state idle dashboard polls
// stay at DEBUG.
const slowRequestThreshold = 5 * time.Second

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: http: https:; connect-src 'self' ws: wss:; font-src 'self' data:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; manifest-src 'self'"

// statusRecorder is a tiny http.ResponseWriter shim that captures the
// final status code written by the wrapped handler. We need this
// because http.ResponseWriter doesn't expose the status after the
// fact, and we want to log it.
//
// It also lazily emits the Server-Timing header right before the
// status line is committed, using whatever entries the request-scoped
// timingCollector has accumulated so far. Done this way (rather than
// after handler return) because Server-Timing must arrive in the
// response *headers*, not as a trailer — adding it after WriteHeader
// is a no-op.
//
// It only overrides the methods we read; everything else stays
// pass-through to the embedded ResponseWriter. http.Hijacker /
// http.Flusher are not implemented here — for SSE we deliberately
// short-circuit before calling ServeHTTP (see noiseSkip).
type statusRecorder struct {
	http.ResponseWriter
	timings     *srvtiming.Collector
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.flushTiming()
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

// Write delegates to the embedded writer but also records the implicit
// 200 that net/http would send if WriteHeader hasn't been called yet.
// Without this, fast 200 responses would log status=0.
func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.flushTiming()
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// Hijack lets WebSocket upgrades (e.g. the live terminal at
// /api/term/ws) take over the underlying TCP connection. Without this
// method the request flows through statusRecorder, which would
// otherwise mask the embedded ResponseWriter's http.Hijacker and cause
// gorilla/websocket's Upgrade() to fail with a 500. We delegate to the
// embedded writer when it supports hijacking.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
}

// Flush delegates to the embedded writer so SSE handlers that flow through
// this middleware (notably the global /api/events stream, which — unlike
// /api/session/{id}/events — is NOT bypassed by noiseSkip) can stream.
// Without this, statusRecorder masks the embedded http.Flusher and the SSE
// handler's `w.(http.Flusher)` check fails with "streaming unsupported",
// so no client ever subscribes and no broadcast is delivered.
func (r *statusRecorder) Flush() {
	if fl, ok := r.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// flushTiming writes the Server-Timing header from the collector, if
// any entries were recorded. Safe to call multiple times — the
// underlying header map deduplicates by key.
func (r *statusRecorder) flushTiming() {
	if r.timings == nil {
		return
	}
	if v := r.timings.Header(); v != "" {
		r.ResponseWriter.Header().Set("Server-Timing", v)
	}
}

// isLoopback returns true if the request originates from localhost.
func isLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	// Strip brackets from IPv6 addresses (e.g. "[::1]" -> "::1")
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host == "127.0.0.1" || host == "::1"
}

// requireLocalhost protects host-control routes from both network and browser
// cross-origin access while preserving Origin-less local CLI/MCP clients.
func (s *Server) requireLocalhost(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isPrivilegedRequest(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// requireLoopbackPeer is requireLocalhost minus the password check: the
// loopback peer address *is* the credential. Used only for /mcp, where
// native MCP clients cannot present an auth cookie and configuring a
// separate token per client is worse than the exposure.
//
// ponytail: behind a reverse proxy that forwards /mcp every request looks
// loopback, so this would be world-reachable. Don't proxy /mcp; if you must,
// block it at the proxy or use a bearer token instead.
func (s *Server) requireLoopbackPeer(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// csrfSafe still applies: a page on another origin must not be
		// able to drive MCP tools from the user's browser.
		if !isLoopback(r) || !s.csrfSafe(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) isPrivilegedRequest(r *http.Request) bool {
	if !isLoopback(r) || !s.csrfSafe(r) {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Origin-less loopback client (CLI, curl, MCP). "Loopback peer"
		// is not a credential behind a reverse proxy: every forwarded
		// request has RemoteAddr 127.0.0.1. Configured auth applies here
		// too, per the documented "auth applies to every client" rule.
		return s.localClientAuthorized(r)
	}
	u, _, _ := parseBrowserOrigin(origin) // csrfSafe already validated it parses
	if isLoopbackHostname(u.Hostname()) {
		return s.localClientAuthorized(r)
	}
	return s.auth != nil && s.auth.hasValidCookie(r)
}

// localClientAuthorized reports whether a loopback client may reach a
// privileged route: always when auth is off or explicitly trusts
// localhost, otherwise only with a valid cookie.
func (s *Server) localClientAuthorized(r *http.Request) bool {
	return s.auth == nil || s.auth.trustLocalhost || s.auth.hasValidCookie(r)
}

// csrfSafe is the browser-origin half of isPrivilegedRequest, without
// the loopback-peer requirement: state-changing requests must not be
// flagged cross-site by fetch metadata, and if an Origin header is
// present it must match the request's own origin (or the configured
// public base URL). Origin-less clients (CLI, curl, MCP) pass. Applied
// to API routes via requireAuth regardless of auth config (#410).
func (s *Server) csrfSafe(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(r.Header.Values("Origin")) != 1 {
		return false
	}
	_, normalized, ok := parseBrowserOrigin(origin)
	if !ok {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	requestOrigin := scheme + "://" + strings.ToLower(r.Host)
	publicOrigin := ""
	if configured, err := url.Parse(s.publicBaseURL); err == nil && configured.Scheme != "" && configured.Host != "" {
		publicOrigin = strings.ToLower(configured.Scheme) + "://" + strings.ToLower(configured.Host)
	}
	return normalized == requestOrigin || normalized == publicOrigin
}

func (s *Server) csrfGuard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !s.csrfSafe(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func parseBrowserOrigin(origin string) (*url.URL, string, bool) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, "", false
	}
	return u, strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func isLoopbackHostname(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func browserOriginMatchesHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, _, ok := parseBrowserOrigin(origin)
	return ok && strings.EqualFold(u.Host, r.Host)
}

// allowedHost reports whether the request's Host header names an
// origin ocman is willing to serve. csrfSafe compares the Origin
// header against the request's *own* Host, so a DNS-rebound page
// (evil.example resolving to 127.0.0.1) is self-consistent and passes
// every origin check — the Host itself has to be validated separately.
//
// Allowed: loopback hostname literals, bare IP literals (a browser
// only sends those when the user typed the IP, so DNS can't be
// rebound onto them), and the host of the configured
// OCMAN_PUBLIC_BASE_URL. Everything else is rejected.
func (s *Server) allowedHost(host string) bool {
	if host == "" {
		return true // HTTP/1.0 client; no DNS name to rebind.
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.Trim(hostname, "[]")
	if isLoopbackHostname(hostname) || net.ParseIP(hostname) != nil {
		return true
	}
	configured, err := url.Parse(s.publicBaseURL)
	if err != nil || configured.Host == "" {
		return false
	}
	return strings.EqualFold(configured.Host, host) || strings.EqualFold(configured.Hostname(), hostname)
}

// withHostAllowlist rejects requests whose Host header is not on the
// allowlist with 421 Misdirected Request. Wraps the entire mux so no
// route — authenticated or not — can be reached via DNS rebinding.
func (s *Server) withHostAllowlist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			log.WithField("host", r.Host).Warn("rejecting request with unrecognized Host header")
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// noiseSkip returns true for paths that we deliberately do not log:
//   - SSE streams (`/api/session/{id}/events`) are long-lived and
//     would either log a single 0ms entry on connect (useless) or
//     produce a misleading "slow" entry on disconnect.
//   - The debug-log sink itself; logging the debug-log endpoint at
//     DEBUG would cause every frontend log call to recurse into
//     another log line.
func noiseSkip(path string) bool {
	if path == "/api/debug/log" {
		return true
	}
	if strings.HasPrefix(path, "/api/session/") && strings.HasSuffix(path, "/events") {
		return true
	}
	return false
}

// withRequestTiming wraps an http.Handler with structured per-request
// timing logs. Fast requests log at DEBUG (so they're invisible in a
// normal `logrus` configuration) and slow requests log at INFO so they
// appear in default production logs without operator action.
//
// Also attaches a per-request timingCollector to the request context
// and emits the accumulated entries as a Server-Timing response
// header so they appear in browser devtools' Timing panel without
// requiring any client-side changes. Adapter / helper code records
// phases via recordTiming(ctx, ...) — see timing.go.
//
// Fields written:
//   - method      — HTTP method ("GET", "POST", ...)
//   - path        — URL path *without* query string (low cardinality
//     so the log can be aggregated by endpoint)
//   - status      — HTTP status code as an int (200, 500, ...)
//   - duration_ms — handler wall-clock latency in milliseconds
//   - timings     — pipe-separated "phase=ms" pairs for slow
//     requests, useful when devtools isn't available
//     (e.g. cron-driven calls or curl)
//
// SSE and the debug-log sink are skipped (see noiseSkip).
func withRequestTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if noiseSkip(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		collector := srvtiming.NewCollector()
		r = r.WithContext(srvtiming.WithCollector(r.Context(), collector))
		rec := &statusRecorder{ResponseWriter: w, timings: collector}
		next.ServeHTTP(rec, r)
		dur := time.Since(start)

		fields := log.Fields{
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      rec.status,
			"duration_ms": dur.Milliseconds(),
		}
		msg := "request served"
		if dur >= slowRequestThreshold {
			if t := collector.Header(); t != "" {
				fields["timings"] = t
			}
			msg = fmt.Sprintf("slow endpoint: %s", r.URL.Path)
		}
		log.WithFields(fields).Debug(msg)
	})
}
