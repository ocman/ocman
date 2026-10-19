package convstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const defaultKeepalive = 15 * time.Second
const streamWriteTimeout = 5 * time.Second

// HandleStream serves GET /api/session/{id}/stream (design §6.6): SSE
// frames "event: oce" with "id: <epoch>:<seq>", resumable with ?after= or
// Last-Event-ID. Headers are sent only once the source connected, so an
// unreachable platform still answers 503 and EventSource stops retrying.
func (m *Module) HandleStream(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		m.serve(w, r, Key{Platform: string(adapter.ID()), SessionID: sessionID}, adapter)
	})
}

func (m *Module) serve(w http.ResponseWriter, r *http.Request, key Key, adapter platforms.Platform) {
	ctx := r.Context()
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	open := func() Source { return m.source(key, adapter) }
	sub, err := m.hub.Subscribe(ctx, key, open, after)
	if err != nil {
		subscribeError(w, key, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	interval := defaultKeepalive
	if m.d.Keepalive != nil {
		interval = m.d.Keepalive()
	}
	out := &sseOut{w: w, rc: http.NewResponseController(w)}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		reason, ok := out.pump(ctx, sub, ticker.C)
		sub.Close()
		if !ok || reason != convevents.ResetSourceRestarted {
			return
		}
		// The source reconnected without continuity: continue on the same
		// response with a fresh hello for the new epoch.
		if sub, err = m.hub.Subscribe(ctx, key, open, ""); err != nil {
			return
		}
	}
}

type sseOut struct {
	w  io.Writer
	rc *http.ResponseController
}

func (o *sseOut) write(b []byte) bool {
	if err := o.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return false
	}
	if _, err := o.w.Write(b); err != nil {
		return false
	}
	return o.rc.Flush() == nil
}

// pump writes the hello, the replay and then live frames until the
// subscription ends. It returns the reset reason and whether the response
// is still writable.
func (o *sseOut) pump(ctx context.Context, sub *Subscription, keepalive <-chan time.Time) (string, bool) {
	if !o.write(sub.Hello.frame) {
		return "", false
	}
	last := sub.Hello.seq
	for _, f := range sub.Replay {
		if !o.write(f.frame) {
			return "", false
		}
		last = f.seq
	}
	for {
		select {
		case <-ctx.Done():
			return "", false
		case f := <-sub.C():
			if !o.write(f.frame) {
				return "", false
			}
			if f.seq > last {
				last = f.seq
			}
		case <-sub.Done():
			reason := sub.Reason()
			if reason == "" {
				return "", false
			}
			return reason, o.write(sub.controlFrame(convevents.StreamReset{Reason: reason}, last))
		case <-keepalive:
			if !o.write([]byte(httpx.SSEKeepalive)) {
				return "", false
			}
		}
	}
}

func subscribeError(w http.ResponseWriter, key Key, err error) {
	fields := log.Fields{"sessionID": key.SessionID, "platform": key.Platform}
	switch {
	case errors.Is(err, context.Canceled):
	case errors.Is(err, platforms.ErrPlatformUnreachable):
		log.WithFields(fields).Debug("conversation stream: no running platform instance; returning 503")
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
	case errors.Is(err, ErrClosed):
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
	case errors.Is(err, platforms.ErrUnsupported):
		http.Error(w, "session events are not supported by this platform", http.StatusNotImplemented)
	case errors.Is(err, platforms.ErrPlatformUnauthenticated):
		log.WithFields(fields).Error("conversation stream: platform authentication failed")
		http.Error(w, "platform authentication failed; check its configured credentials", http.StatusBadGateway)
	default:
		log.WithFields(fields).WithError(err).Warn("conversation stream: source failed before connecting")
		http.Error(w, "session event source failed", http.StatusBadGateway)
	}
}
