package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/convstream"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// convStreamModule serves the Ocman conversation event stream, the only
// per-session browser stream. Its live source is the owner pipeline: local
// sessions run the auto-approve tee and sinks; remote sessions are bridged
// from the owner's forwarded v1 bytes.
func (s *Server) convStreamModule() *convstream.Module {
	return s.convStreamMod.get(func() *convstream.Module {
		return convstream.NewModule(convstream.Deps{
			WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, string, platforms.Platform)) {
				s.withSessionAdapter(w, r, fn)
			},
			Proxy:      s.proxyConversationSource,
			Descendant: s.isDescendantSession,
			Queue: func(ctx context.Context, key convstream.Key) ([]convevents.QueuedMessage, error) {
				views, err := s.queueModule().Queued(ctx, key.Platform, key.SessionID)
				if err != nil {
					return nil, err
				}
				out := make([]convevents.QueuedMessage, len(views))
				for i, v := range views {
					out[i] = convevents.QueuedMessage(v)
				}
				return out, nil
			},
			Keepalive:  func() time.Duration { return sseKeepaliveInterval },
			CaptureDir: os.Getenv(convstream.CaptureEnv),
			Guards: module.Guards{
				Auth:      func(h http.HandlerFunc) http.HandlerFunc { return s.requireAuth(traceSessionStream(h)) },
				Localhost: s.requireLocalhost,
			},
		})
	})
}

// proxyConversationSource feeds the hub's v1 mapper. The hub forwards a
// remote's stream without a judge (AD-14) and only observes its git command
// hints; local hints come from the headless auto-approve watcher instead.
func (s *Server) proxyConversationSource(ctx context.Context, sessionID string, adapter platforms.Platform, raw, synthetic io.Writer, flush func()) error {
	if isRemotePlatformID(string(adapter.ID())) {
		platformID := string(adapter.ID())
		tee := &autoapprove.Tee{W: raw, Flush: flush, OnGitCommand: func(id, action string) {
			s.broadcastGitCommand(ctx, platformID, id, action)
		}}
		return adapter.ProxyEvents(ctx, sessionID, tee, flush)
	}
	return s.proxyOwnerSessionEvents(ctx, sessionID, adapter, raw, synthetic, flush)
}

// traceSessionStream gives each conversation stream connection one span and
// counts it as an open SSE connection; otelhttp skips these long-lived routes.
func traceSessionStream(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := telemetry.Tracer().Start(r.Context(), "GET /api/session/{id}/stream",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("ocman.session_id", r.PathValue("id")),
				attribute.String("http.route", "/api/session/{id}/stream"),
			),
		)
		defer span.End()
		if sseActiveConnections != nil {
			sseActiveConnections.Add(ctx, 1)
			defer sseActiveConnections.Add(ctx, -1)
		}
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) isDescendantSession(ctx context.Context, root convstream.Key, child string) bool {
	adapter, ok := s.registry.Get(platforms.ID(root.Platform))
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for child != "" && !seen[child] && len(seen) < 256 {
		if child == root.SessionID {
			return true
		}
		seen[child] = true
		row, err := platforms.ReadSessionSummary(ctx, adapter, child)
		if err != nil || row == nil || row.ID != child {
			return false
		}
		child = row.ParentID
	}
	return false
}
