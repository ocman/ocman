package autoapprove

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
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
	// /global/event stream closing (clean EOF, network error, or OpenCode
	// shutdown) and the watcher reconnecting. Short enough that a
	// real OpenCode restart doesn't leave a noticeable auto-approve
	// gap; long enough that a hard-down OpenCode doesn't spin the
	// loop and burn CPU.
	autoApproveReconnectDelay = 2 * time.Second
)

// autoApproveWatcher drives the headless auto-approve pipeline by
// subscribing directly to each running OpenCode instance's /global/event SSE
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
			reason := "replied"
			if reply == "reject" {
				reason = "rejected"
			}
			svc.broadcastPermissionResolved(sessionID, permissionID, reason)
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
			if adapter, ok := w.svc.OpencodeAdapter().(*opencode.Adapter); ok {
				adapter.ClearPromptsForPort(port)
				// Forget this instance's turn states too: sessions it
				// was running are now unobservable, which is what
				// settles an unfinished turn as interrupted instead of
				// leaving it busy forever.
				adapter.ClearSessionStatusForPort(port)
			}
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

// subscribe holds a single long-lived /global/event SSE connection to the
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

// streamOnce opens one /global/event SSE connection and feeds bytes into a
// Tee until the connection ends. Returns when the
// upstream closes (nil on clean EOF, error otherwise) or when ctx is
// cancelled.
func (w *autoApproveWatcher) streamOnce(ctx context.Context, port string) error {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/global/event", port)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build /global/event request: %w", err)
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect /global/event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("/global/event upstream HTTP %d", resp.StatusCode)
	}

	// Resolve the OpenCode adapter once per stream; if the registry
	// doesn't have it (e.g. ocman was started with -platforms="" or a
	// non-opencode platform set), callbacks will see a nil adapter and
	// Ensure will fail safe by short-circuiting. The
	// platform ID is still propagated so logs are useful.
	adapter := w.svc.OpencodeAdapter()
	ocAdapter, _ := adapter.(*opencode.Adapter)
	portGeneration := uint64(0)
	statusGeneration := uint64(0)
	firstReconciliationFinished := make(chan struct{})
	if ocAdapter != nil {
		portGeneration = ocAdapter.PromptPortGeneration(port)
		// Seed the live turn state from the instance's own snapshot
		// before the stream can tell us anything. Without this a
		// session that was already mid-turn when ocman connected (or
		// restarted) would have no live entry, and the status would
		// fall back to message-shape inference. The seed is what makes
		// the live view survive an ocman restart without ocman
		// persisting a copy of state OpenCode owns.
		statusGeneration = ocAdapter.StatusPortGeneration(port)
		if !ocAdapter.SeedSessionStatusFromInstance(streamCtx, port, statusGeneration) {
			logger := log.WithField("port", port)
			logger.Debug("autoapprove-watcher: /session/status snapshot unavailable, turn state stays unobserved")
		}
		directories := w.directoriesForPort(port)
		onPermission := func(prompt platforms.LivePrompt) {
			if w.onPermission == nil {
				return
			}
			sessionID, _ := prompt["sessionID"].(string)
			permissionID, _ := prompt["id"].(string)
			permission, _ := prompt["permission"].(string)
			metadata, _ := prompt["metadata"].(map[string]any)
			patterns := promptStrings(prompt["patterns"])
			if sessionID != "" && permissionID != "" {
				w.onPermission(opencode.PlatformID, adapter, sessionID, permissionID, permission, patterns, metadata)
				if w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
					w.svc.deps.BroadcastSessionChanged(sessionID)
				}
			}
		}
		first := ocAdapter.StartPromptReconciliation(ctx, port, directories, onPermission)
		go func() {
			ok := <-first
			close(firstReconciliationFinished)
			for !ok {
				select {
				case <-streamCtx.Done():
					return
				case <-time.After(w.reconnectDelay):
				}
				ok = <-ocAdapter.StartPromptReconciliation(streamCtx, port, directories, onPermission)
			}
		}()
	} else {
		close(firstReconciliationFinished)
	}

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
		OnPromptAsked: func(directory, kind string, prompt platforms.LivePrompt) {
			if ocAdapter != nil {
				ocAdapter.ObservePromptAskedFromPort(port, portGeneration, directory, kind, prompt)
			}
			if sessionID, _ := prompt["sessionID"].(string); sessionID != "" && w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
				w.svc.deps.BroadcastSessionChanged(sessionID)
			}
		},
		OnPromptResolved: func(directory, kind, sessionID, requestID string) {
			if ocAdapter != nil {
				ocAdapter.ObservePromptResolved(directory, kind, sessionID, requestID)
			}
			if sessionID != "" && w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
				w.svc.deps.BroadcastSessionChanged(sessionID)
			}
		},
		OnQuestionResolved: func(sessionID, requestID, reason string) {
			if w.svc != nil && w.svc.deps.BroadcastQuestionResolved != nil {
				w.svc.deps.BroadcastQuestionResolved(sessionID, requestID, reason)
			}
		},
		OnSessionStatus: func(sessionID, statusType string) {
			if ocAdapter == nil {
				return
			}
			ocAdapter.ObserveSessionStatus(port, statusGeneration, sessionID, statusType)
			w.broadcastSessionStatus(ocAdapter, port, sessionID, statusType)
		},
		OnSessionIdle: func(sessionID string) {
			// session.idle is the same edge as session.status=idle, but
			// OpenCode emits it separately; record it so a missed
			// status event can't leave the session pinned busy.
			if ocAdapter != nil {
				ocAdapter.ObserveSessionStatus(port, statusGeneration, sessionID, "idle")
				w.broadcastSessionStatus(ocAdapter, port, sessionID, "idle")
			}
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
	<-firstReconciliationFinished
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("read /global/event: %w", err)
	}
	return nil
}

func (w *autoApproveWatcher) broadcastSessionStatus(adapter *opencode.Adapter, port, sessionID, statusType string) {
	if w.svc == nil || w.svc.deps.BroadcastSessionStatus == nil {
		return
	}
	status := db.StatusBusy
	if statusType == "idle" {
		var err error
		status, err = adapter.SessionStatusOnPort(sessionID, port)
		if err != nil {
			if w.svc.deps.BroadcastSessionChanged != nil {
				w.svc.deps.BroadcastSessionChanged(sessionID)
			}
			return
		}
	}
	w.svc.deps.BroadcastSessionStatus(sessionID, status)
}

func promptStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func (w *autoApproveWatcher) directoriesForPort(port string) []string {
	byDirectory := w.discoverPorts()
	directories := make([]string, 0, len(byDirectory))
	for directory, candidate := range byDirectory {
		if candidate == port {
			directories = append(directories, directory)
		}
	}
	return directories
}

// RunWatcher is the top-level entry point invoked by the server's
// StartOnListener. It blocks until ctx is cancelled.
func (s *Service) RunWatcher(ctx context.Context) {
	w := newAutoApproveWatcher(s)
	w.run(ctx)
}
