package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// --- Session-scoped SSE event stream ---

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		s.serveSessionEvents(w, r, sessionID, adapter)
	})
}

func (s *Server) serveSessionEvents(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	ctx, span := telemetry.Tracer().Start(r.Context(), "GET /api/session/{id}/events",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("ocman.session_id", sessionID),
			attribute.String("ocman.platform", string(adapter.ID())),
			attribute.String("http.route", "/api/session/{id}/events"),
		),
	)
	defer span.End()

	if sseActiveConnections != nil {
		sseActiveConnections.Add(ctx, 1)
		defer sseActiveConnections.Add(ctx, -1)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Wrap the writer so we can detect whether ProxyEvents ever produced
	// any output. When the platform is unreachable (no live OpenCode
	// instance for this session's directory), ProxyEvents returns
	// ErrPlatformUnreachable before writing a single byte. Without this
	// wrapper, the deferred status would be 200 + an empty body, which
	// the browser's EventSource treats as a successful stream that ended
	// cleanly — it then reconnects every ~500ms in a tight loop and
	// starves the HTTP/1.1 connection pool, blocking all other API
	// requests on the same origin.
	//
	// By keeping the status header unsent until the first real Write,
	// we can still emit HTTP 503 if ProxyEvents fails before producing
	// output. EventSource treats non-200 responses as a hard failure
	// and stops reconnecting — the connection slot is freed and the UI
	// recovers.
	lw := &lazyHeaderWriter{ResponseWriter: w}

	// Every write to the ResponseWriter — from the tee, from a sink, and
	// the accompanying flushes — goes through lw so they all take one
	// lock. Keep flush nil when the writer can't stream so callers that
	// branch on it behave as before.
	var flush func()
	if _, ok := w.(http.Flusher); ok {
		flush = lw.Flush
	}

	// For remote sessions, auto-approve is the owner's responsibility
	// (AD-14): the remote runs the judge with its own settings and emits
	// ocman.permission.* events into the stream, which the gRPC tunnel
	// forwards verbatim. The hub must NOT tee a remote stream into its
	// own judge, so we write events straight through.
	var err error
	if isRemotePlatformID(string(adapter.ID())) {
		err = adapter.ProxyEvents(ctx, sessionID, lw, flush)
	} else {
		err = s.proxyOwnerSessionEvents(ctx, sessionID, adapter, lw, lw, flush)
	}
	if err == nil {
		span.SetStatus(codes.Ok, "stream ended")
		return
	}
	if errors.Is(err, context.Canceled) {
		span.AddEvent("client disconnected")
		span.SetStatus(codes.Ok, "client disconnected")
		return
	}
	if errors.Is(err, platforms.ErrSSEIdleTimeout) {
		span.AddEvent("SSE idle timeout — client will reconnect")
		span.SetStatus(codes.Ok, "idle timeout")
		return
	}
	// Platform unreachable before any bytes flowed: send a real 503 so
	// EventSource gives up and frees the socket. Logged at Debug —
	// this is a normal steady state when OpenCode isn't running for
	// the session's directory (e.g. right after a machine reboot),
	// not a fault worth a WARN.
	if errors.Is(err, platforms.ErrPlatformUnreachable) && !lw.Wrote() {
		span.AddEvent("platform unreachable — returning 503")
		span.SetStatus(codes.Ok, "platform unreachable")
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		log.WithFields(log.Fields{"sessionID": sessionID}).
			Debug("SSE proxy: no running platform instance; returning 503")
		return
	}
	if errors.Is(err, ocapi.ErrAuthentication) && !lw.Wrote() {
		span.RecordError(err)
		span.SetStatus(codes.Error, "OpenCode authentication failed")
		http.Error(w, "OpenCode authentication failed; check the configured server password", http.StatusBadGateway)
		log.WithField("sessionID", sessionID).Error("SSE proxy: OpenCode authentication failed")
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
		Warn("SSE proxy stream ended with error")
}

// ProxyRemoteSessionEvents runs the owner-side autoapprove tee and synthetic
// event sink before bytes enter the gRPC tunnel. The hub forwards them only.
func (s *Server) ProxyRemoteSessionEvents(ctx context.Context, _ string, sessionID string, adapter platforms.Platform, rawWriter, syntheticWriter io.Writer, flush func()) error {
	return s.proxyOwnerSessionEvents(ctx, sessionID, adapter, rawWriter, syntheticWriter, flush)
}

func (s *Server) proxyOwnerSessionEvents(ctx context.Context, sessionID string, adapter platforms.Platform, rawWriter, syntheticWriter io.Writer, flush func()) error {
	sink := s.aaSvc().RegisterSink(sessionID, syntheticWriter, flush)
	defer s.aaSvc().UnregisterSink(sessionID, sink)

	tee := &autoapprove.Tee{
		W:     rawWriter,
		Flush: flush,
		OnPermission: func(evtSessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
			s.aaSvc().Ensure(adapter.ID(), adapter, evtSessionID, permissionID, permission, patterns, metadata)
		},
		OnPermissionReplied: func(evtSessionID, permissionID, reply string) {
			s.aaSvc().HandlePermissionReplied(ctx, evtSessionID, permissionID, reply)
		},
	}
	return adapter.ProxyEvents(ctx, sessionID, tee, flush)
}

// lazyHeaderWriter delays the implicit 200 OK status write until the
// first real Write call. This lets serveSessionEvents emit a non-200
// status when ProxyEvents fails before producing any output (e.g. no
// live OpenCode instance for the session's directory).
//
// WriteHeader is forwarded directly so anything that explicitly sets a
// status (the 503 fast-path above) bypasses the wrapper entirely.
//
// It is also the serialisation point for the SSE response body. Two
// independent goroutines write to it: the request goroutine via
// Tee -> ProxyEvents, and the auto-approve background goroutine via the
// registered Sink. Sink.mu only serialises sinks against each other,
// not against ProxyEvents, so without this mutex the `wrote` bool races
// and two SSE frames can interleave mid-write.
type lazyHeaderWriter struct {
	http.ResponseWriter
	mu    sync.Mutex
	wrote bool
}

func (l *lazyHeaderWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.wrote && len(p) > 0 {
		l.wrote = true
	}
	return l.ResponseWriter.Write(p)
}

// Wrote reports whether any bytes have reached the client yet.
func (l *lazyHeaderWriter) Wrote() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wrote
}

// Flush forwards to the underlying ResponseWriter when it supports
// http.Flusher. SSE relies on Flush after every event, so this
// must work even when no bytes have been written yet. Takes the same
// lock as Write so a flush can't land between two halves of a frame.
func (l *lazyHeaderWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f, ok := l.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
