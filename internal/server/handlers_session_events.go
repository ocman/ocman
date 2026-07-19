package server

import (
	"context"
	"errors"
	"io"
	"net/http"

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

	flusher, _ := w.(http.Flusher)
	var flush func()
	if flusher != nil {
		flush = flusher.Flush
	}

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

	// Register this writer so non-SSE code paths (REST permission
	// listing, prompt resurrection on session re-open) can push
	// synthetic ocman.permission.* events into the same connection.
	// Both the tee's onPermission callback and handleSessionPermissions
	// flow through ensureAutoApprove → emitPermissionPending → this sink.
	// The deferred unregister both removes the registry entry and marks
	// the sink closed, so any in-flight backgroundAutoApprove emit
	// turns into a no-op rather than panicking on a recycled writer.
	sink := s.aaSvc().RegisterSink(sessionID, lw, flush)
	defer s.aaSvc().UnregisterSink(sessionID, sink)

	// Tee the SSE stream so permission.asked events trigger server-side
	// auto-approve. This is one of two entry points into the
	// auto-approve pipeline; the other is runAutoApproveWatcher, which
	// keeps the pipeline running headlessly when no browser tab is
	// open. Both flow through ensureAutoApprove, which deduplicates
	// against in-flight goroutines so only one judge ever runs per
	// permission.
	//
	// OpenCode's /event stream is process-wide — every event for every
	// session in that OpenCode process flows through this connection.
	// The callback's `evtSessionID` argument carries the *event's*
	// session ID (extracted from the payload) so the auto-approve
	// pipeline routes the verdict, the persistence, and the
	// ocman.permission.* SSE event back to the correct session.
	// Using the connection's `sessionID` for routing was a bug — it
	// attributed every other session's auto-approved notice to
	// whichever session the user was currently viewing.
	tee := &autoapprove.Tee{
		W:     lw,
		Flush: flush,
		OnPermission: func(evtSessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
			s.aaSvc().Ensure(adapter.ID(), adapter, evtSessionID, permissionID, permission, patterns, metadata)
		},
		// permission.replied fires when the user (or any non-ocman
		// client, e.g. the OpenCode TUI) answers the prompt. Cancel
		// any in-flight judge so we stop polling immediately and the
		// verdict — if it arrives later — is discarded before it can
		// race the user's answer. A "Allow always" reply is also
		// captured into the parent's shadow allowlist (issue #101).
		OnPermissionReplied: func(evtSessionID, permissionID, reply string) {
			s.aaSvc().HandlePermissionReplied(evtSessionID, permissionID, reply)
		},
	}

	// For remote sessions, auto-approve is the owner's responsibility
	// (AD-14): the remote runs the judge with its own settings and emits
	// ocman.permission.* events into the stream, which the gRPC tunnel
	// forwards verbatim. The hub must NOT tee a remote stream into its
	// own judge, so we write events straight through.
	var dst io.Writer = tee
	if isRemotePlatformID(string(adapter.ID())) {
		dst = lw
	}

	err := adapter.ProxyEvents(ctx, sessionID, dst, flush)
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
	if errors.Is(err, platforms.ErrPlatformUnreachable) && !lw.wrote {
		span.AddEvent("platform unreachable — returning 503")
		span.SetStatus(codes.Ok, "platform unreachable")
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		log.WithFields(log.Fields{"sessionID": sessionID}).
			Debug("SSE proxy: no running platform instance; returning 503")
		return
	}
	if errors.Is(err, ocapi.ErrAuthentication) && !lw.wrote {
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

// lazyHeaderWriter delays the implicit 200 OK status write until the
// first real Write call. This lets serveSessionEvents emit a non-200
// status when ProxyEvents fails before producing any output (e.g. no
// live OpenCode instance for the session's directory).
//
// Once `wrote` flips to true the wrapper is a transparent pass-through;
// only the first Write needs the bookkeeping. WriteHeader is forwarded
// directly so anything that explicitly sets a status (the 503 fast-
// path below) bypasses the wrapper entirely.
type lazyHeaderWriter struct {
	http.ResponseWriter
	wrote bool
}

func (l *lazyHeaderWriter) Write(p []byte) (int, error) {
	if !l.wrote && len(p) > 0 {
		l.wrote = true
	}
	return l.ResponseWriter.Write(p)
}

// Flush forwards to the underlying ResponseWriter when it supports
// http.Flusher. SSE relies on Flush after every event, so this
// must work even when no bytes have been written yet.
func (l *lazyHeaderWriter) Flush() {
	if f, ok := l.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
