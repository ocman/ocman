package server

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
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

// requireLocalhost wraps a handler to reject non-loopback requests.
func requireLocalhost(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
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
