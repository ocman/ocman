package autoapprove

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// waitTimeout is deliberately generous: these tests coordinate real
// goroutines and HTTP connections, so a tight budget only buys flakes on
// a loaded CI runner. A passing run exits as soon as the condition holds.
const waitTimeout = 10 * time.Second

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
	if r.URL.Path != "/global/event" {
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

func wrappedPermissionAskedEvent(directory, sessionID, permissionID string) string {
	return "data: " + fmt.Sprintf(
		`{"directory":%q,"payload":{"type":"permission.asked","properties":{"id":%q,"sessionID":%q,"permission":"Bash","patterns":[],"metadata":{}}}}`,
		directory, permissionID, sessionID,
	) + "\n\n"
}

func TestAutoApproveWatcher_UsesOneGlobalStreamForSharedPort(t *testing.T) {
	var globalHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/event" {
			http.NotFound(w, r)
			return
		}
		globalHits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(wrappedPermissionAskedEvent("/repo/worktree", "ses-global", "perm-global")))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	asks := make(chan string, 1)
	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo": port, "/repo/worktree": port}
	}
	w.onPermission = func(_ platforms.ID, _ platforms.Platform, sessionID, permissionID, _ string, _ []string, _ map[string]any) {
		asks <- sessionID + "/" + permissionID
	}
	w.rescanInterval = time.Second
	w.reconnectDelay = time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	select {
	case got := <-asks:
		if got != "ses-global/perm-global" {
			t.Fatalf("permission = %q, want ses-global/perm-global", got)
		}
	case <-time.After(time.Second):
		t.Fatal("wrapped permission was not dispatched")
	}
	if got := globalHits.Load(); got != 1 {
		t.Fatalf("global stream connections = %d, want 1", got)
	}
}

func TestAutoApproveWatcherUpdatesPromptRegistryFromWrappedEvents(t *testing.T) {
	events := []string{
		`{"directory":"/repo/a","payload":{"type":"permission.asked","properties":{"id":"perm-1","sessionID":"ses-1","permission":"Bash"}}}`,
		`{"directory":"/repo/a","payload":{"type":"permission.rejected","properties":{"requestID":"perm-1","sessionID":"ses-1"}}}`,
		`{"directory":"/repo/a","payload":{"type":"question.asked","properties":{"id":"question-rejected","sessionID":"ses-1","questions":[]}}}`,
		`{"directory":"/repo/a","payload":{"type":"question.rejected","properties":{"requestID":"question-rejected","sessionID":"ses-1"}}}`,
		`{"directory":"/repo/a","payload":{"type":"question.asked","properties":{"id":"question-replied","sessionID":"ses-1","questions":[]}}}`,
		`{"directory":"/repo/a","payload":{"type":"question.replied","properties":{"requestID":"question-replied","sessionID":"ses-1"}}}`,
		`{"directory":"/repo/a","payload":{"type":"question.asked","properties":{"id":"question-pending","sessionID":"ses-1","questions":[]}}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()

	adapter := opencode.New(nil, nil)
	var changed atomic.Int32
	svc := NewService(Deps{
		OpencodePlatform:        func() platforms.Platform { return adapter },
		BroadcastSessionChanged: func(string) { changed.Add(1) },
	})
	w := newAutoApproveWatcher(svc)
	if err := w.streamOnce(context.Background(), strings.TrimPrefix(server.URL, "http://127.0.0.1:")); err != nil {
		t.Fatalf("streamOnce: %v", err)
	}

	permissions, _ := adapter.ListPermissions(context.Background(), "ses-1")
	if len(permissions) != 0 {
		t.Fatalf("permissions = %#v, want replied permission removed", permissions)
	}
	questions, _ := adapter.ListQuestions(context.Background(), "ses-1")
	if len(questions) != 1 || questions[0]["id"] != "question-pending" {
		t.Fatalf("questions = %#v, want question-pending", questions)
	}
	if got := changed.Load(); got != int32(len(events)) {
		t.Fatalf("session change broadcasts = %d, want %d prompt edges", got, len(events))
	}
}

func TestAutoApproveWatcherBroadcastsRejectedPermission(t *testing.T) {
	var got string
	svc := NewService(Deps{BroadcastPermissionResolved: func(_, _, reason string) { got = reason }})
	w := newAutoApproveWatcher(svc)
	w.onPermissionReplied("ses-1", "perm-1", "reject")
	if got != "rejected" {
		t.Fatalf("resolution reason = %q, want rejected", got)
	}
}

func TestAutoApproveWatcherConnectsBeforePromptReconciliation(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/global/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/question" && r.URL.Query().Get("directory") == "/repo/a" {
			_, _ = w.Write([]byte(`[{"id":"q-snapshot","sessionID":"ses-snapshot","questions":[]}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	adapter := opencode.New(nil, nil)
	svc := NewService(Deps{OpencodePlatform: func() platforms.Platform { return adapter }})
	w := newAutoApproveWatcher(svc)
	w.discoverPorts = func() map[string]string {
		return map[string]string{"/repo/a": port, "/repo/b": port}
	}
	if err := w.streamOnce(context.Background(), port); err != nil {
		t.Fatalf("streamOnce: %v", err)
	}
	testutil.WaitFor(t, waitTimeout, "the question snapshot to be reconciled", func() bool {
		questions, _ := adapter.ListQuestions(context.Background(), "ses-snapshot")
		return len(questions) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 || paths[0] != "/global/event" {
		t.Fatalf("request order = %v, want /global/event first", paths)
	}
}

func TestAutoApproveWatcherReconcilesPendingPermissionIntoAutoApprove(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", map[bool]string{true: "text/event-stream", false: "application/json"}[r.URL.Path == "/global/event"])
		if r.URL.Path == "/permission" {
			_, _ = w.Write([]byte(`[{"id":"perm-snapshot","sessionID":"ses-snapshot","permission":"Bash","patterns":[],"metadata":{}}]`))
			return
		}
		if r.URL.Path != "/global/event" {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	adapter := opencode.New(nil, nil)
	svc := NewService(Deps{OpencodePlatform: func() platforms.Platform { return adapter }})
	w := newAutoApproveWatcher(svc)
	w.discoverPorts = func() map[string]string { return map[string]string{"/repo/a": port} }
	got := make(chan string, 1)
	w.onPermission = func(_ platforms.ID, _ platforms.Platform, sessionID, permissionID, _ string, _ []string, _ map[string]any) {
		got <- sessionID + "/" + permissionID
	}
	if err := w.streamOnce(context.Background(), port); err != nil {
		t.Fatalf("streamOnce: %v", err)
	}
	select {
	case value := <-got:
		if value != "ses-snapshot/perm-snapshot" {
			t.Fatalf("reconciled permission = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("pending snapshot did not enter auto-approve")
	}
}

func TestAutoApproveWatcherRetriesFailedReconciliationOnHealthyStream(t *testing.T) {
	releaseStream := make(chan struct{})
	var permissionHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-releaseStream
			return
		}
		if r.URL.Path == "/permission" && permissionHits.Add(1) <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/permission" {
			_, _ = w.Write([]byte(`[{"id":"perm-retried","sessionID":"ses-retried","permission":"Bash","patterns":[],"metadata":{}}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	adapter := opencode.New(nil, nil)
	svc := NewService(Deps{OpencodePlatform: func() platforms.Platform { return adapter }})
	w := newAutoApproveWatcher(svc)
	w.discoverPorts = func() map[string]string { return map[string]string{"/repo/retry": port} }
	w.reconnectDelay = time.Millisecond
	got := make(chan string, 1)
	w.onPermission = func(_ platforms.ID, _ platforms.Platform, sessionID, permissionID, _ string, _ []string, _ map[string]any) {
		got <- sessionID + "/" + permissionID
	}
	done := make(chan error, 1)
	go func() { done <- w.streamOnce(context.Background(), port) }()
	select {
	case value := <-got:
		if value != "ses-retried/perm-retried" {
			t.Fatalf("retried permission=%q", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failed reconciliation was not retried")
	}
	close(releaseStream)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAutoApproveWatcherAuthenticatesSSE(t *testing.T) {
	const password = "watcher-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(permissionAskedEvent("ses-auth", "perm-auth", "Bash", "pwd")))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	svc := NewService(Deps{OpenCodeAuth: ocapi.New(password)})
	w := newAutoApproveWatcher(svc)
	called := false
	w.onPermission = func(_ platforms.ID, _ platforms.Platform, _, _, _ string, _ []string, _ map[string]any) {
		called = true
	}
	if err := w.streamOnce(context.Background(), port); err != nil {
		t.Fatalf("streamOnce: %v", err)
	}
	if !called {
		t.Fatal("authenticated watcher did not process event")
	}

	bad := newAutoApproveWatcher(NewService(Deps{OpenCodeAuth: ocapi.New("wrong")}))
	if err := bad.streamOnce(context.Background(), port); !errors.Is(err, ocapi.ErrAuthentication) {
		t.Fatalf("invalid watcher credential = %v, want authentication error", err)
	}
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

	testutil.WaitFor(t, waitTimeout, "the watcher to subscribe to the port", func() bool {
		return atomic.LoadInt32(&fake.conns) >= 1
	})

	// Port disappears.
	ports.Store(map[string]string{})

	// The watcher must cancel its sub on the next tick.
	testutil.WaitFor(t, waitTimeout, "the subscription to close after the port disappeared", func() bool {
		return atomic.LoadInt32(&fake.conns) == 0
	})
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

	// Expect at least 3 connections, confirming reconnect is happening.
	testutil.WaitFor(t, waitTimeout, "at least 3 reconnects", func() bool {
		return atomic.LoadInt32(&fake.totalHit) >= 3
	})
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

	testutil.WaitFor(t, waitTimeout, "the watcher to subscribe", func() bool {
		return atomic.LoadInt32(&fake.conns) >= 1
	})

	cancel()

	testutil.WaitFor(t, waitTimeout, "all connections to close after cancel", func() bool {
		return atomic.LoadInt32(&fake.conns) == 0
	})
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

	// Wait for the first subscription deterministically, so a loaded
	// runner can't turn "slow to connect" into "deduped".
	testutil.WaitFor(t, waitTimeout, "the watcher to subscribe", func() bool {
		return atomic.LoadInt32(&fake.conns) >= 1
	})

	// Then let several rescan ticks fire. This one has to be a sleep:
	// the assertion is that *no* extra connection appears, and absence
	// of an event can't be polled for.
	time.Sleep(20 * w.rescanInterval)

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

	// Deliberate sleep, not a missing WaitFor: the point is to let cancel
	// race the first tick rather than land before it. There is no
	// condition to poll for, and the assertion below is a bounded select,
	// so a mistimed sleep exercises a different path but never flakes.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunWatcher did not exit within 2s of context cancel")
	}
}

// TestAutoApproveWatcher_RoutesToEnsureAutoApproveWithNoClients is an
// integration-style sanity check: with no browser clients or SSE sinks,
// constructing the watcher with a real Service still routes the permission
// to the judge path. The
// watcher's default onPermission resolves the OpenCode adapter via
// deps.OpencodePlatform and calls svc.Ensure (which short-circuits
// in this test because we pre-seed the autoApprove cache, proving the
// call went through the expected path).
func TestAutoApproveWatcher_RoutesToEnsureAutoApproveWithNoClients(t *testing.T) {
	fake := newFakeOpenCodeEventServer(nil)
	fake.setEvents([]string{
		permissionAskedEvent("ses-A", "perm-route", "Bash command", "rm -rf /"),
	}, true)
	defer fake.close()

	// Provide a fake adapter with the OpenCode platform ID so the
	// watcher's default onPermission can resolve it.
	fp := &fakePlatform{id: "opencode"}
	srv := &Service{
		autoApprove: make(map[string]*autoApproveStatus),
		deps: Deps{
			OpencodePlatform: func() platforms.Platform { return fp },
			DefaultEnabled:   true,
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

	deadline := time.Now().Add(2 * time.Second)
	for {
		if verdict, ok := srv.lookupJudged("ses-A", "perm-route"); ok && verdict == verdictUnsafe {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("headless permission did not reach the auto-approve engine")
		}
		time.Sleep(10 * time.Millisecond)
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
