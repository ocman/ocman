package autoapprove

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// fakeOpenCodeEventServer is a minimal test double for OpenCode's
// HTTP server, specifically the /event SSE endpoint. It streams a
// pre-canned sequence of SSE events to every connecting client.
//
// Each Write to the connection is followed by a flush so the
// downstream Tee sees the bytes immediately. The server
// keeps the connection open until the client disconnects OR the test
// calls Close().
type fakeOpenCodeEventServer struct {
	srv      *httptest.Server
	events   []string // each entry is a complete SSE event (including trailing \n\n)
	mu       sync.Mutex
	conns    int32 // number of currently-open /event connections
	totalHit int32 // total /event connections ever opened (for reconnect tests)

	// holdOpen, when true, prevents the server from closing the
	// connection after streaming the canned events. Default false:
	// connections close after the last event, which lets reconnect
	// tests observe a fresh connection on each iteration.
	holdOpen bool
}

func newFakeOpenCodeEventServer(events []string) *fakeOpenCodeEventServer {
	f := &fakeOpenCodeEventServer{events: events}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeOpenCodeEventServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/event" {
		http.NotFound(w, r)
		return
	}
	atomic.AddInt32(&f.conns, 1)
	atomic.AddInt32(&f.totalHit, 1)
	defer atomic.AddInt32(&f.conns, -1)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	f.mu.Lock()
	evts := append([]string(nil), f.events...)
	hold := f.holdOpen
	f.mu.Unlock()

	for _, e := range evts {
		if _, err := w.Write([]byte(e)); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if !hold {
		return
	}
	// Hold open until the client cancels.
	<-r.Context().Done()
}

// port returns the numeric port of the fake server.
func (f *fakeOpenCodeEventServer) port() string {
	u, _ := url.Parse(f.srv.URL)
	return u.Port()
}

func (f *fakeOpenCodeEventServer) close() { f.srv.Close() }

// addEvents replaces the canned event list. Newly opened connections
// see the new events; existing connections keep their original list.
func (f *fakeOpenCodeEventServer) setEvents(evts []string, hold bool) {
	f.mu.Lock()
	f.events = evts
	f.holdOpen = hold
	f.mu.Unlock()
}

// permissionAskedEvent returns a serialised SSE event matching the
// OpenCode /event default-channel envelope shape with the embedded
// "type" field. This is what Tee.dispatchEvent expects
// when the named-channel header is absent.
func permissionAskedEvent(sessionID, permissionID, permission, command string) string {
	return "data: " + fmt.Sprintf(
		`{"id":"evt_x","type":"permission.asked","properties":{"id":%q,"sessionID":%q,"permission":%q,"patterns":[],"metadata":{"command":%q}}}`,
		permissionID, sessionID, permission, command,
	) + "\n\n"
}

// TestAutoApproveWatcher_FiresOnPermissionWithoutFrontend is the
// primary regression: with no SSE client connected (no browser tab),
// the watcher must still drive Ensure when OpenCode emits
// permission.asked. This was the bug — the legacy path only fired
// when a frontend session opened /api/session/{id}/events.
func TestAutoApproveWatcher_FiresOnPermissionWithoutFrontend(t *testing.T) {
	fake := newFakeOpenCodeEventServer([]string{
		permissionAskedEvent("ses-A", "perm-1", "Bash command", "rm bla"),
	})
	fake.setEvents([]string{
		permissionAskedEvent("ses-A", "perm-1", "Bash command", "rm bla"),
	}, true) // hold open so the watcher doesn't reconnect-flap during the test
	defer fake.close()

	// Capture Ensure calls by intercepting at the seam: we
	// install a fake onPermission that mirrors what the real wiring
	// does. The watcher pushes events through a tee whose onPermission
	// is exactly svc.Ensure — so the test instead asks
	// the watcher to use a custom onPermission callback.
	type ask struct {
		sessionID    string
		permissionID string
		permission   string
		patterns     []string
		metadata     map[string]any
	}
	asks := make(chan ask, 4)

	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo/a": fake.port()}
	}
	w.onPermission = func(_ platforms.ID, _ platforms.Platform, sessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
		asks <- ask{sessionID, permissionID, permission, patterns, metadata}
	}
	w.rescanInterval = 10 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	select {
	case got := <-asks:
		if got.sessionID != "ses-A" || got.permissionID != "perm-1" {
			t.Errorf("got asked=%+v, want sessionID=ses-A permissionID=perm-1", got)
		}
		if cmd, _ := got.metadata["command"].(string); cmd != "rm bla" {
			t.Errorf("metadata.command = %v, want %q", got.metadata["command"], "rm bla")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not call onPermission within 2s")
	}
}

// TestAutoApproveWatcher_CancelsSubscriptionWhenPortDisappears verifies
// that the per-port goroutine is torn down when the OpenCode process
// stops listening (e.g. user killed the TUI). The next tick's
// discoverPorts no longer reports the port; the watcher must cancel
// the subscription's context so the goroutine exits.
func TestAutoApproveWatcher_CancelsSubscriptionWhenPortDisappears(t *testing.T) {
	fake := newFakeOpenCodeEventServer(nil)
	fake.setEvents(nil, true)
	defer fake.close()

	var ports atomic.Value // map[string]string
	ports.Store(map[string]string{"/repo/a": fake.port()})

	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string {
		return ports.Load().(map[string]string)
	}
	w.rescanInterval = 20 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	// Wait for the watcher to subscribe to the port.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&fake.conns) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&fake.conns) < 1 {
		t.Fatal("watcher did not subscribe within 2s")
	}

	// Port disappears.
	ports.Store(map[string]string{})

	// The watcher must cancel its sub on the next tick.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&fake.conns) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c := atomic.LoadInt32(&fake.conns); c != 0 {
		t.Errorf("expected 0 open connections after port disappeared, got %d", c)
	}
}

// TestAutoApproveWatcher_ReconnectsAfterUpstreamClose verifies that when
// the OpenCode /event stream closes (clean EOF or error), the watcher
// reconnects after reconnectDelay. Without this, a transient OpenCode
// restart would silently disable headless auto-approve until the next
// rescan tick (which is fine but slower).
func TestAutoApproveWatcher_ReconnectsAfterUpstreamClose(t *testing.T) {
	// Fake server closes the connection after sending one heartbeat-
	// style empty SSE comment (no permission events). Each new
	// connection ticks totalHit.
	fake := newFakeOpenCodeEventServer([]string{": heartbeat\n\n"})
	// holdOpen=false → server closes after one write. Watcher must reconnect.
	defer fake.close()

	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo/a": fake.port()}
	}
	w.rescanInterval = 1 * time.Second // doesn't matter for this test
	w.reconnectDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	// Expect at least 3 connections within 2 seconds, confirming
	// reconnect is happening.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&fake.totalHit) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hits := atomic.LoadInt32(&fake.totalHit); hits < 3 {
		t.Errorf("expected at least 3 reconnects within 2s, got %d", hits)
	}
}

// TestAutoApproveWatcher_StopsOnContextCancel verifies clean shutdown:
// when the parent context is cancelled, every per-port subscription
// goroutine must exit and no upstream connection must remain open.
func TestAutoApproveWatcher_StopsOnContextCancel(t *testing.T) {
	fake := newFakeOpenCodeEventServer(nil)
	fake.setEvents(nil, true)
	defer fake.close()

	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo/a": fake.port()}
	}
	w.rescanInterval = 20 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go w.run(ctx)

	// Wait for the watcher to subscribe.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&fake.conns) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&fake.conns) < 1 {
		t.Fatal("watcher did not subscribe within 2s")
	}

	cancel()

	// All connections must close within a short window.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&fake.conns) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c := atomic.LoadInt32(&fake.conns); c != 0 {
		t.Errorf("expected 0 open connections after cancel, got %d", c)
	}
}

// TestAutoApproveWatcher_DedupesSamePort verifies that a port already
// being subscribed to is not double-subscribed when discoverPorts keeps
// returning it across ticks. Exactly one connection must remain open.
func TestAutoApproveWatcher_DedupesSamePort(t *testing.T) {
	fake := newFakeOpenCodeEventServer(nil)
	fake.setEvents(nil, true)
	defer fake.close()

	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo/a": fake.port()}
	}
	w.rescanInterval = 10 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	// Let several ticks fire.
	time.Sleep(200 * time.Millisecond)

	if c := atomic.LoadInt32(&fake.conns); c != 1 {
		t.Errorf("expected exactly 1 open connection, got %d", c)
	}
}

// TestServer_RunAutoApproveWatcher_RespectsContext verifies the
// Service-level entry point exits cleanly when the parent context is
// cancelled. This is the function StartOnListener spawns; if it
// blocked on shutdown, ocman would never terminate cleanly.
func TestServer_RunAutoApproveWatcher_RespectsContext(t *testing.T) {
	srv := &Service{
		autoApprove: make(map[string]*autoApproveStatus),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.RunWatcher(ctx)
		close(done)
	}()

	// Give the watcher a moment to actually start up so cancel races
	// the first tick rather than running before it.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunWatcher did not exit within 2s of context cancel")
	}
}

// TestAutoApproveWatcher_RoutesToEnsureAutoApprove is an integration-
// style sanity check: when constructed with a real Service, the
// watcher's default onPermission resolves the OpenCode adapter via
// deps.OpencodePlatform and calls svc.Ensure (which short-circuits
// in this test because we pre-seed the autoApprove cache, proving the
// call went through the expected path).
func TestAutoApproveWatcher_RoutesToEnsureAutoApprove(t *testing.T) {
	fake := newFakeOpenCodeEventServer(nil)
	fake.setEvents([]string{
		permissionAskedEvent("ses-A", "perm-route", "Bash command", "ls"),
	}, true)
	defer fake.close()

	// Provide a fake adapter with the OpenCode platform ID so the
	// watcher's default onPermission can resolve it.
	fp := &fakePlatform{id: "opencode"}
	srv := &Service{
		autoApprove: make(map[string]*autoApproveStatus),
		deps: Deps{
			OpencodePlatform: func() platforms.Platform { return fp },
		},
	}

	// Pre-seed the cache so Ensure short-circuits and we
	// don't accidentally spawn a real judge goroutine. We then watch
	// for the short-circuit to happen by polling lookupJudged — once
	// the watcher has driven its way through Ensure for
	// our permissionID, we're done.
	//
	// Tactic: install a sentinel verdict for a DIFFERENT permission
	// first, leaving perm-route un-cached. The watcher's call should
	// land on perm-route, Ensure will try to claim, and
	// (since the adapter is a fake that does nothing) the goroutine
	// returns quickly. We assert that Ensure was reached
	// by observing the in-flight slot transition from empty -> claimed
	// -> released.
	//
	// To avoid timing flakiness, we instead intercept via the seam:
	// override w.onPermission to call svc.Ensure and then
	// signal a channel. Same as the first test, but we want to assert
	// it ran against the real wiring (newAutoApproveWatcher(srv) with
	// no override) — so check that w.onPermission is non-nil by
	// default and that calling w.onPermission with our adapter
	// resolves correctly.
	w := newAutoApproveWatcher(srv)
	if w.onPermission == nil {
		t.Fatal("newAutoApproveWatcher(srv) left onPermission nil; want a default")
	}

	got := make(chan string, 1)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo/a": fake.port()}
	}
	// Wrap the real callback so we observe it firing.
	real := w.onPermission
	w.onPermission = func(pid platforms.ID, adapter platforms.Platform, sessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
		real(pid, adapter, sessionID, permissionID, permission, patterns, metadata)
		got <- string(pid) + "|" + sessionID + "|" + permissionID
	}
	w.rescanInterval = 10 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	select {
	case key := <-got:
		if !strings.HasPrefix(key, "opencode|ses-A|perm-route") {
			t.Errorf("default onPermission routed wrong key: %q", key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("default onPermission did not fire within 2s")
	}
}

// TestHandleSessionChangedDedup verifies that session.updated handling
// only acts on the first sighting of a session ID (so per-turn/token
// updates don't repeatedly bust the cache and broadcast), and that an
// empty ID is ignored. svc is nil so broadcast is skipped; we assert
// on the seen-set, which is the dedup gate.
func TestHandleSessionChangedDedup(t *testing.T) {
	w := newAutoApproveWatcher(nil)

	seen := func() int {
		w.seenMu.Lock()
		defer w.seenMu.Unlock()
		return len(w.seenSessions)
	}

	w.handleSessionChanged("")
	if seen() != 0 {
		t.Fatalf("empty session ID was recorded; seen=%d", seen())
	}

	w.handleSessionChanged("ses-1")
	w.handleSessionChanged("ses-1") // duplicate: must be a no-op
	w.handleSessionChanged("ses-2")
	if got := seen(); got != 2 {
		t.Fatalf("seen sessions = %d, want 2 (ses-1 deduped)", got)
	}
	if _, ok := w.seenSessions["ses-1"]; !ok {
		t.Error("ses-1 not recorded")
	}
	if _, ok := w.seenSessions["ses-2"]; !ok {
		t.Error("ses-2 not recorded")
	}
}
