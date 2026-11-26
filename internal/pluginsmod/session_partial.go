package pluginsmod

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/convevents/v1map"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type sessionPreviewKey struct{ platform, session string }
type sessionPreviewWatch struct{ cancel context.CancelFunc }

func (m *Module) stopSessionPreview(platformID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := sessionPreviewKey{platformID, sessionID}
	if watch := m.sessionPreviews[key]; watch != nil {
		watch.cancel()
		delete(m.sessionPreviews, key)
	}
}

// watchSessionPreview gives the stream a short head start before sending the
// prompt. An unavailable stream or slow handshake never fails admission.
func (m *Module) watchSessionPreview(ctx context.Context, pluginID, platformID, sessionID, previous string, requested bool) context.CancelFunc {
	noop := func() {}
	if !requested || m.deps.Adapter == nil || !m.sessionCallbackAllowed(ctx, pluginID, plugins.SessionPartialAllowed) {
		return noop
	}
	owner, linked, err := m.plugins.GetPluginSessionPlugin(ctx, platformID, sessionID)
	if err != nil || !linked || owner != pluginID {
		return noop
	}
	adapter, ok := m.deps.Adapter(ctx, platformID, sessionID)
	if !ok || !adapter.Capabilities().Events {
		return noop
	}
	m.mu.Lock()
	key := sessionPreviewKey{platformID, sessionID}
	if m.stopping || len(m.sessionPreviews) >= 32 {
		m.mu.Unlock()
		return noop
	}
	if m.sessionPreviews == nil {
		m.sessionPreviews = make(map[sessionPreviewKey]*sessionPreviewWatch)
	}
	if _, watching := m.sessionPreviews[key]; watching {
		m.mu.Unlock()
		return noop
	}
	previewCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ready := make(chan struct{})
	watch := &sessionPreviewWatch{cancel: cancel}
	m.sessionPreviews[key] = watch
	if !m.workers.Go(func(lifetime context.Context) {
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		defer func() {
			m.mu.Lock()
			if m.sessionPreviews[key] == watch {
				delete(m.sessionPreviews, key)
			}
			m.mu.Unlock()
		}()
		module.RunWithRecover("plugin-session-preview", func() {
			m.streamSessionPreview(previewCtx, adapter, key, pluginID, previous, sync.OnceFunc(func() { close(ready) }))
		})
	}) {
		delete(m.sessionPreviews, key)
		cancel()
	}
	m.mu.Unlock()
	grace := time.NewTimer(200 * time.Millisecond)
	defer grace.Stop()
	select {
	case <-ready:
	case <-previewCtx.Done():
	case <-ctx.Done():
	case <-grace.C:
	}
	return cancel
}

func (m *Module) streamSessionPreview(ctx context.Context, adapter platforms.Platform, key sessionPreviewKey, pluginID, previous string, ready func()) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	wireID, err := pluginSessionReference(ctx, adapter, key.session)
	if err != nil {
		return
	}
	// One latest snapshot, regardless of provider speed or callback latency.
	updates := make(chan previewUpdate, 1)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		defer cancel()
		module.RunWithRecover("plugin-session-preview-source", func() {
			writer := &sessionPreviewWriter{projection: v1map.AssistantPreview{
				SessionID: key.session, PreviousMessageID: previous, MaxBytes: plugins.SessionMaxPartialBytes,
			}, updates: updates, stop: cancel}
			_ = adapter.ProxyEvents(ctx, key.session, &httpx.SSEFrameWriter{Dst: writer}, ready)
		})
	}()
	defer func() { cancel(); <-closed }()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var sequence uint64
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			select {
			case update := <-updates:
				turnKey := wireID + ":" + update.message
				if recorded, err := m.plugins.HasPluginSessionCompletion(ctx, pluginID, turnKey); err != nil || recorded {
					continue
				}
				sequence++
				turnID := sessionTurnID(turnKey)
				_ = m.SessionBroker().Partial(ctx, pluginID, fmt.Sprintf("preview:%s:%d", turnID, sequence), plugins.SessionPartial{
					SessionID: wireID, TurnID: turnID, Text: update.text,
				})
			default:
			}
		}
	}
}

type previewUpdate struct{ message, text string }

type sessionPreviewWriter struct {
	projection v1map.AssistantPreview
	updates    chan previewUpdate
	stop       context.CancelFunc
}

func (w *sessionPreviewWriter) Write(frame []byte) (int, error) {
	id, text, done := w.projection.Apply(v1map.ParseFrame(frame))
	if done {
		w.stop()
		return len(frame), context.Canceled
	}
	if id != "" {
		select {
		case <-w.updates:
		default:
		}
		w.updates <- previewUpdate{id, text}
	}
	return len(frame), nil
}
