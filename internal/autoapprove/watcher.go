package autoapprove

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

const (
	// autoApproveRescanInterval is how often the watcher re-runs
	// OpenCode port discovery to pick up newly-started or
	// newly-stopped instances. 10 s matches the lsof cache TTL: any
	// faster would only return cached data, any slower would delay
	// auto-approve for sessions started after ocman boot.
	autoApproveRescanInterval = 10 * time.Second

	// autoApproveReconnectDelay is the cooldown between an upstream
	// /event stream closing (clean EOF, network error, or OpenCode
	// shutdown) and the watcher reconnecting. Short enough that a
	// real OpenCode restart doesn't leave a noticeable auto-approve
	// gap; long enough that a hard-down OpenCode doesn't spin the
	// loop and burn CPU.
	autoApproveReconnectDelay = 2 * time.Second
)

// autoApproveWatcher drives the headless auto-approve pipeline by
// subscribing directly to each running OpenCode instance's /event SSE
// stream, independent of any frontend connection.
//
// The legacy flow only fired auto-approve when a browser tab was open
// on the relevant session (because the tee that observed
// permission.asked only lived inside the frontend-driven SSE handler).
// This watcher closes that gap: it keeps one connection per OpenCode
// process for the lifetime of the ocman server, so headless
// background sessions stay covered.
//
// Dedup: every event is routed through Server.Ensure which
// already deduplicates against in-flight goroutines and already-judged
// permissions — so if a frontend tab is also open (and the existing
// per-session SSE tee fires for the same permission), only one judge
// runs.
type autoApproveWatcher struct {
	// svc is the parent Service. May be nil in tests that drive
	// onPermission manually.
	svc *Service

	// discoverPorts returns the directory -> port map for all
	// running OpenCode instances. Seam for tests; defaults to
	// opencode.DiscoverOpenCodePorts.
	discoverPorts func() map[string]string

	// onPermission is called for every permission.asked event seen on
	// any subscribed upstream. The default routes through
	// Server.Ensure with the OpenCode platform adapter
	// resolved from the registry. Tests can override to observe calls.
	onPermission func(platformID platforms.ID, adapter platforms.Platform,
		sessionID, permissionID, permission string, patterns []string,
		metadata map[string]any)

	// onPermissionReplied is called for every permission.replied event
	// so any in-flight judge for that permission is cancelled — the
	// user has already answered the prompt (e.g. via the OpenCode
	// TUI) and we must drop our verdict to avoid double-answering. reply
	// is the user's choice so a "Allow always" reply can be captured
	// into the parent's shadow allowlist (issue #101).
	onPermissionReplied func(sessionID, permissionID, reply string)

	// httpClient is used for the long-lived /event SSE GET. Defaults
	// to an http.Client with no timeout (SSE streams are long-lived).
	httpClient *http.Client

	// rescanInterval and reconnectDelay are exposed so tests can run
	// the loops on tight timings without changing the production
	// constants.
	rescanInterval time.Duration
	reconnectDelay time.Duration

	// mu guards subs.
	mu sync.Mutex
	// subs maps port -> cancel func for the per-port subscription
	// goroutine. Entries are added on tick() when a new port appears
	// and removed (cancel called) when the port disappears or the
	// parent context is cancelled.
	subs map[string]context.CancelFunc

	// seenMu guards seenSessions.
	seenMu sync.Mutex
	// seenSessions records session IDs we've already surfaced via
	// onSessionChanged. OpenCode emits session.updated per turn/token,
	// so without this set we'd bust the cache + broadcast on every
	// keystroke of every active session. We only care about the first
	// sighting (= "a session appeared"); subsequent updates are noise
	// for the list view. ponytail: unbounded set, fine for the session
	// count on one machine; switch to an LRU if it ever isn't.
	seenSessions map[string]struct{}
}

// newAutoApproveWatcher constructs a watcher wired against the real
// OpenCode port discovery and the given Service. svc may be nil in
// tests; in that case the default onPermission is a no-op so callers
// must inject their own.
func newAutoApproveWatcher(svc *Service) *autoApproveWatcher {
	auth := ocapi.New("")
	if svc != nil {
		auth = svc.deps.OpenCodeAuth
	}
	w := &autoApproveWatcher{
		svc:            svc,
		discoverPorts:  opencode.DiscoverOpenCodePorts,
		httpClient:     &http.Client{Transport: auth.Transport(http.DefaultTransport)}, // no timeout — SSE is long-lived
		rescanInterval: autoApproveRescanInterval,
		reconnectDelay: autoApproveReconnectDelay,
		subs:           make(map[string]context.CancelFunc),
		seenSessions:   make(map[string]struct{}),
	}

	// Default onPermission routes through Ensure, which
	// owns dedup, the configured delay, the judge call, and the
	// post-verdict persistence + SSE emit. The adapter and platform
	// ID are passed verbatim so the persistence layer scopes the
	// approval to the right platform.
	if svc != nil {
		w.onPermission = func(platformID platforms.ID, adapter platforms.Platform,
			sessionID, permissionID, permission string, patterns []string,
			metadata map[string]any) {
			svc.Ensure(platformID, adapter, sessionID, permissionID, permission, patterns, metadata)
		}
		w.onPermissionReplied = func(sessionID, permissionID, reply string) {
			// Cancels any in-flight judge and captures "Allow always"
			// replies into the parent's shadow allowlist (issue #101).
			svc.HandlePermissionReplied(sessionID, permissionID, reply)
			// Broadcast the resolution so cross-page prompt toasts clear
			// instantly even when the user answered from the OpenCode TUI
			// or another browser tab. The watcher is always connected, so
			// this fires regardless of which (if any) tab is open.
			svc.broadcastPermissionResolved(sessionID, permissionID, "replied")
		}
	}
	return w
}

// run is the watcher's top-level loop. It runs tick() once immediately
// (so subscriptions start without waiting for the first ticker) and
// then on every rescanInterval until ctx is cancelled. On cancel it
// tears down every per-port subscription and returns.
//
// Wrapped in runWithRecover so a panic in a tick body never kills the
// whole loop. Per-port subscription goroutines have their own recover.
func (w *autoApproveWatcher) run(ctx context.Context) {
	runWithRecover("autoapprove-watcher", func() { w.tick(ctx) })

	ticker := time.NewTicker(w.rescanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.shutdown()
			return
		case <-ticker.C:
			runWithRecover("autoapprove-watcher", func() { w.tick(ctx) })
		}
	}
}

// tick reconciles the current set of subscriptions with the set of
// ports reported by discoverPorts:
//   - Any newly-appeared port spawns a subscription goroutine.
//   - Any disappeared port has its subscription cancelled.
//   - Already-subscribed ports that are still present are left
//     untouched (no reconnect — the existing connection is fine).
func (w *autoApproveWatcher) tick(parentCtx context.Context) {
	live := livePortSet(w.discoverPorts())

	w.mu.Lock()
	defer w.mu.Unlock()

	// Cancel subs whose ports are no longer reported.
	for port, cancel := range w.subs {
		if _, stillUp := live[port]; !stillUp {
			cancel()
			delete(w.subs, port)
		}
	}

	// Subscribe to any new port.
	for port := range live {
		if _, already := w.subs[port]; already {
			continue
		}
		subCtx, cancel := context.WithCancel(parentCtx)
		w.subs[port] = cancel
		go w.subscribe(subCtx, port)
	}
}

// livePortSet collapses discoverPorts's directory -> port map into a
// set of currently-listening ports. Empty ports are dropped so a
// malformed discovery entry can't create a phantom subscription.
func livePortSet(byDir map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(byDir))
	for _, port := range byDir {
		if port == "" {
			continue
		}
		out[port] = struct{}{}
	}
	return out
}

// shutdown cancels every per-port subscription. Called from run when
// the parent context fires. Holding mu for the iteration is fine —
// the cancel funcs themselves are O(1) and the subs goroutines exit
// asynchronously after the lock is released.
func (w *autoApproveWatcher) shutdown() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for port, cancel := range w.subs {
		cancel()
		delete(w.subs, port)
	}
}

// subscribe holds a single long-lived /event SSE connection to the
// OpenCode instance on the given port. It reuses Tee to
// parse permission.asked / permission.replied events out of the byte
// stream and routes them to onPermission / onPermissionReplied.
//
// On any read error, clean EOF, or upstream connection close, it sleeps
// reconnectDelay and tries again. The loop exits when ctx is cancelled.
func (w *autoApproveWatcher) subscribe(ctx context.Context, port string) {
	logger := log.WithField("port", port)
	logger.Debug("autoapprove-watcher: subscribing")
	defer logger.Debug("autoapprove-watcher: subscription ended")

	// Each iteration is wrapped in runWithRecover so a panic in
	// streamOnce (e.g. an unexpected SSE shape) doesn't kill the
	// subscription — we just sleep and reconnect on the next tick.
	for {
		if ctx.Err() != nil {
			return
		}
		runWithRecover("autoapprove-watcher-sub", func() {
			if err := w.streamOnce(ctx, port); err != nil && ctx.Err() == nil {
				logger.WithError(err).Debug("autoapprove-watcher: stream ended, will reconnect")
			}
		})
		// Brief cooldown before reconnecting. Honours ctx so shutdown
		// doesn't have to wait the full delay.
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.reconnectDelay):
		}
	}
}

// handleSessionChanged reacts to an upstream session.updated event.
// It dedupes on the session ID so per-turn/per-token updates of an
// already-known session are ignored; only the first sighting of a
// session ID busts the sessions cache and broadcasts session.changed,
// which is exactly what makes a freshly-created session appear without
// waiting out the list-poll / refresher latency.
func (w *autoApproveWatcher) handleSessionChanged(sessionID string) {
	if sessionID == "" {
		return
	}
	w.seenMu.Lock()
	if _, ok := w.seenSessions[sessionID]; ok {
		w.seenMu.Unlock()
		return
	}
	w.seenSessions[sessionID] = struct{}{}
	w.seenMu.Unlock()

	// First time we've seen this session: surface it now.
	opencode.InvalidateSessionsCache()
	if w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
		w.svc.deps.BroadcastSessionChanged(sessionID)
	}
}

// streamOnce opens one /event SSE connection and feeds bytes into a
// Tee until the connection ends. Returns when the
// upstream closes (nil on clean EOF, error otherwise) or when ctx is
// cancelled.
func (w *autoApproveWatcher) streamOnce(ctx context.Context, port string) error {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/event", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build /event request: %w", err)
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect /event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("/event upstream HTTP %d", resp.StatusCode)
	}

	// Resolve the OpenCode adapter once per stream; if the registry
	// doesn't have it (e.g. ocman was started with -platforms="" or a
	// non-opencode platform set), callbacks will see a nil adapter and
	// Ensure will fail safe by short-circuiting. The
	// platform ID is still propagated so logs are useful.
	adapter := w.svc.OpencodeAdapter()

	tee := &Tee{
		W:     io.Discard, // we only need the parsing side; nothing else consumes this stream
		Flush: nil,
		OnPermission: func(sessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
			if w.onPermission != nil {
				w.onPermission(opencode.PlatformID, adapter, sessionID, permissionID, permission, patterns, metadata)
			}
		},
		OnPermissionReplied: func(sessionID, permissionID, reply string) {
			if w.onPermissionReplied != nil {
				w.onPermissionReplied(sessionID, permissionID, reply)
			}
		},
		OnQuestionResolved: func(sessionID, requestID, reason string) {
			if w.svc != nil && w.svc.deps.BroadcastQuestionResolved != nil {
				w.svc.deps.BroadcastQuestionResolved(sessionID, requestID, reason)
			}
		},
		OnSessionIdle: func(sessionID string) {
			if w.svc != nil && w.svc.deps.BroadcastSessionIdle != nil {
				w.svc.deps.BroadcastSessionIdle(sessionID)
			}
		},
		OnSessionChanged: w.handleSessionChanged,
	}

	// Copy bytes through the tee until the stream ends. io.Copy
	// returns nil on clean EOF and a non-nil error on read failure or
	// ctx cancel (the request context propagates to the body reader).
	_, err = io.Copy(tee, resp.Body)
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("read /event: %w", err)
	}
	return nil
}

// RunWatcher is the top-level entry point invoked by the server's
// StartOnListener. It blocks until ctx is cancelled.
func (s *Service) RunWatcher(ctx context.Context) {
	w := newAutoApproveWatcher(s)
	w.run(ctx)
}
