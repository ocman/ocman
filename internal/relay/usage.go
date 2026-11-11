package relay

import (
	"context"
	"net/http"

	"github.com/ocman/ocman/internal/usagewire"
)

// UsageForwarder exports usage and security as separate OTLP log events. The
// relay holds no durable queue, usage database, deduplication IDs or IP log files.
type UsageForwarder interface {
	Forward(ctx context.Context, report *usagewire.Report, ip string, status int) error
}

type usageCollector struct {
	server   *Server
	ingest   *rateLimiter
	security *rateLimiter
	inFlight chan struct{}
}

func newUsageCollector(server *Server) *usageCollector {
	return &usageCollector{server: server,
		ingest:   newRateLimiter(60, 10, server.cfg.Now),
		security: newRateLimiter(60, 10, server.cfg.Now), inFlight: make(chan struct{}, 32)}
}

func (c *usageCollector) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/usage", c.collect)
}

func (c *usageCollector) collect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if c.server.cfg.Usage == nil {
		http.Error(w, "usage forwarding unavailable", http.StatusServiceUnavailable)
		return
	}
	ip := c.server.clientKey(r)
	if !c.ingest.allow(ip) {
		c.reject(w, r, ip, http.StatusTooManyRequests, "too many usage reports")
		return
	}
	var report usagewire.Report
	decoded := &usageResponse{ResponseWriter: w}
	if !usagewire.ReadJSON(decoded, r, &report) {
		c.securityEvent(r.Context(), ip, decoded.status)
		return
	}
	if !report.Valid(c.server.cfg.Now()) {
		c.reject(w, r, ip, http.StatusBadRequest, "invalid usage report")
		return
	}
	if err := c.forward(r.Context(), &report, ip, http.StatusNoContent); err != nil {
		http.Error(w, "usage forwarding unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *usageCollector) reject(w http.ResponseWriter, r *http.Request, ip string, status int, message string) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	c.securityEvent(r.Context(), ip, status)
	http.Error(w, message, status)
}

func (c *usageCollector) securityEvent(ctx context.Context, ip string, status int) {
	if c.security.allow(ip) {
		_ = c.forward(ctx, nil, ip, status) // Bound abuse evidence without exporting payloads.
	}
}

func (c *usageCollector) forward(ctx context.Context, report *usagewire.Report, ip string, status int) error {
	select {
	case c.inFlight <- struct{}{}:
		defer func() { <-c.inFlight }()
		return c.server.cfg.Usage.Forward(ctx, report, ip, status)
	default:
		return context.DeadlineExceeded // Never queue an unbounded export backlog.
	}
}

type usageResponse struct {
	http.ResponseWriter
	status int
}

func (w *usageResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *usageResponse) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
