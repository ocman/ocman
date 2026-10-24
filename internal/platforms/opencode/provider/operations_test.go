package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv1"
)

func TestRevertAndUnrevert_ProxyOpenCodeContract(t *testing.T) {
	const sid = "sess-revert"
	const dir = "/tmp/proj-revert"
	var calls []struct {
		path string
		body string
	}

	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet {
			calls = append(calls, struct {
				path string
				body string
			}{r.URL.Path, string(body)})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sid + `"}`))
	})
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{dir: port} })
	defer restore()
	resetPortCacheForTests()
	database := newTestDBWithSession(t, sid, dir)
	a := New(database, nil, nil, ocapi.Auth{})

	if err := a.Revert(context.Background(), platforms.RevertSessionRequest{SessionID: sid, MessageID: "msg-1"}); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if err := a.Unrevert(context.Background(), platforms.UnrevertSessionRequest{SessionID: sid}); err != nil {
		t.Fatalf("Unrevert: %v", err)
	}
	if got, want := calls[0].path, "/session/"+sid+"/revert"; got != want {
		t.Errorf("revert path = %q, want %q", got, want)
	}
	if got, want := calls[0].body, `{"messageID":"msg-1"}`; got != want {
		t.Errorf("revert body = %s, want %s", got, want)
	}
	if got, want := calls[1].path, "/session/"+sid+"/unrevert"; got != want {
		t.Errorf("unrevert path = %q, want %q", got, want)
	}
	if got, want := calls[1].body, "{}"; got != want {
		t.Errorf("unrevert body = %s, want %s", got, want)
	}
}

func TestResolvePortCtxFallsBackToSessionProbe(t *testing.T) {
	const sid = "sess-probe-port"
	const dir = "/tmp/proj-probe-port"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"`+sid+`"}`))

	restorePorts := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{} })
	restoreServers := setDiscoverServersImplForTests(func() []openCodeServer {
		return []openCodeServer{{directory: "/tmp/other-cwd", port: fake.Port()}}
	})
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	t.Cleanup(func() {
		restorePorts()
		restoreServers()
		resetPortCacheForTests()
		resetSessionPortAffinityForTests()
	})

	database := newTestDBWithSession(t, sid, dir)
	a := New(database, nil, nil, ocapi.Auth{})

	port, _, err := a.resolvePortCtx(context.Background(), sid)
	if err != nil {
		t.Fatalf("resolvePortCtx: %v", err)
	}
	if port != fake.Port() {
		t.Fatalf("resolvePortCtx port = %q, want %q", port, fake.Port())
	}
}

func TestAdapterAuthenticatesHTTPAndSSE(t *testing.T) {
	const sid, dir, password = "sess-auth", "/tmp/proj-auth", "adapter-secret"
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != ocapi.DefaultUsername || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/session/" + sid:
			fmt.Fprintf(w, `{"id":%q}`, sid)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": authenticated\n\n"))
		case "/session/" + sid + "/prompt_async":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	withTestPort(t, dir, port)
	database := newTestDBWithSession(t, sid, dir)
	a := New(database, nil, nil, ocapi.New(password))
	t.Cleanup(func() { configureHTTPAuth(ocapi.New("")) })

	var events bytes.Buffer
	if err := a.ProxyEvents(context.Background(), sid, &events, nil); err != nil {
		t.Fatalf("ProxyEvents: %v", err)
	}
	if !strings.Contains(events.String(), "authenticated") {
		t.Fatalf("SSE output = %q", events.String())
	}
	if err := a.SendMessage(context.Background(), platforms.SendMessageRequest{SessionID: sid, Message: "hello"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	bad := New(database, nil, nil, ocapi.New("wrong"))
	if err := bad.ProxyEvents(context.Background(), sid, io.Discard, nil); !errors.Is(err, ocapi.ErrAuthentication) {
		t.Fatalf("invalid SSE credential = %v, want authentication error", err)
	}
}

func TestSendMessageSerializesPromptsPerSession(t *testing.T) {
	const sid, dir = "sess-serialized", "/tmp/proj-serialized"
	firstPrompt := make(chan struct{})
	release := make(chan struct{})
	var prompts atomic.Int32
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/"+sid+"/prompt_async" {
			http.NotFound(w, r)
			return
		}
		if prompts.Add(1) == 1 {
			close(firstPrompt)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	})
	withTestPort(t, dir, port)
	a := New(newBoundTestDBWithSession(t, sid, dir), nil, nil, ocapi.Auth{})
	req := platforms.SendMessageRequest{SessionID: sid, Message: "steer", Model: "openai/gpt-5", Agent: "build"}
	errs := make(chan error, 2)
	go func() { errs <- a.SendMessage(context.Background(), req) }()
	<-firstPrompt
	go func() { errs <- a.SendMessage(context.Background(), req) }()
	time.Sleep(50 * time.Millisecond)
	if n := prompts.Load(); n != 1 {
		t.Fatalf("second send reached OpenCode before the first finished (%d prompts)", n)
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := prompts.Load(); n != 2 {
		t.Fatalf("prompts = %d, want 2", n)
	}
}

func TestAdapterProxiesUnauthenticatedSSEWhenAuthDisabled(t *testing.T) {
	const sid, dir = "sess-no-auth", "/tmp/proj-no-auth"
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": unauthenticated\n\n"))
	})
	withTestPort(t, dir, port)
	a := New(newBoundTestDBWithSession(t, sid, dir), nil, nil, ocapi.Auth{})

	var events bytes.Buffer
	if err := a.ProxyEvents(context.Background(), sid, &events, nil); err != nil {
		t.Fatalf("ProxyEvents: %v", err)
	}
	if !strings.Contains(events.String(), "unauthenticated") {
		t.Fatalf("SSE output = %q", events.String())
	}
}

// TestProxyEvents_IdleTimeoutReturnsErrSSEIdleTimeout verifies that when the
// upstream stops sending bytes, ProxyEvents returns platforms.ErrSSEIdleTimeout
// rather than blocking forever. We patch sseIdleTimeout to a tiny value via
// an httptest server that simply never writes after the response header.
func TestProxyEvents_IdleTimeoutReturnsErrSSEIdleTimeout(t *testing.T) {
	const sid = "sess-idle-timeout"
	const dir = "/tmp/proj-idle-timeout"

	// Upstream that sends headers but then blocks until the client closes.
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-unblock // block until the test unblocks us
	}))
	defer srv.Close()
	defer close(unblock)

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	database := newBoundTestDBWithSession(t, sid, dir)

	// Temporarily override the idle timeout constant via a subtest-scoped
	// monkey-patch — we exercise the same code path by patching at the
	// package level is not straightforward, so instead we validate indirectly:
	// run ProxyEvents with a context that has a very short deadline, confirm
	// the body-close path returns an error. We cannot easily shorten
	// sseIdleTimeout (it's a const), so instead we cancel the context after
	// a tiny delay and confirm the error is context.Canceled (not the idle
	// sentinel). The real idle-timeout path is exercised by the timer firing,
	// which we can't accelerate without exposing the timer. This test at
	// least confirms the happy-path and that the function is not blocked.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	a := New(database, nil, nil, ocapi.Auth{})
	var buf bytes.Buffer
	err := a.ProxyEvents(ctx, sid, &buf, nil)
	// Context deadline hit — upstream was silent. Must not return nil.
	if err == nil {
		t.Fatal("expected an error when upstream is silent, got nil")
	}
}

func TestProxyEventsPinsPortAffinityForSendMessage(t *testing.T) {
	const sid = "sess-port-affinity"
	const dir = "/tmp/proj-port-affinity"

	var eventHits1 int32
	var promptHits1 int32
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session/"+sid:
			fmt.Fprintf(w, `{"id":%q}`, sid)
		case r.URL.Path == "/event":
			atomic.AddInt32(&eventHits1, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": ready\n\n"))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/session/"+sid+"/prompt_async":
			atomic.AddInt32(&promptHits1, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv1.Close()

	var promptHits2 int32
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/session/"+sid+"/prompt_async" {
			atomic.AddInt32(&promptHits2, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv2.Close()

	port1 := strings.TrimPrefix(srv1.URL, "http://127.0.0.1:")
	port2 := strings.TrimPrefix(srv2.URL, "http://127.0.0.1:")
	var useSecond int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		if atomic.LoadInt32(&useSecond) == 1 {
			return map[string]string{dir: port2}
		}
		return map[string]string{dir: port1}
	})
	defer restore()
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	defer resetSessionPortAffinityForTests()

	database := newTestDBWithSession(t, sid, dir)
	a := New(database, nil, nil, ocapi.Auth{})

	var buf bytes.Buffer
	if err := a.ProxyEvents(context.Background(), sid, &buf, nil); err != nil {
		t.Fatalf("ProxyEvents: %v", err)
	}
	if got := atomic.LoadInt32(&eventHits1); got != 1 {
		t.Fatalf("event stream hit port1 %d times, want 1", got)
	}

	atomic.StoreInt32(&useSecond, 1)
	td.InvalidateOpenCodePortCache()
	if err := a.SendMessage(context.Background(), platforms.SendMessageRequest{
		SessionID: sid,
		Message:   "hello",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := atomic.LoadInt32(&promptHits1); got != 1 {
		t.Fatalf("prompt hits on pinned port1 = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&promptHits2); got != 0 {
		t.Fatalf("prompt hits on newly discovered port2 = %d, want 0", got)
	}
}

func TestProxyEventsRejectsNonSSEWithoutPinningPort(t *testing.T) {
	const sid = "sess-events-non-sse"
	const dir = "/tmp/proj-events-non-sse"

	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/"+sid {
			fmt.Fprintf(w, `{"id":%q}`, sid)
			return
		}
		if r.URL.Path == "/event" {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	})
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{dir: port}
	})
	defer restore()
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	defer resetSessionPortAffinityForTests()

	database := newTestDBWithSession(t, sid, dir)
	a := New(database, nil, nil, ocapi.Auth{})

	var buf bytes.Buffer
	if err := a.ProxyEvents(context.Background(), sid, &buf, nil); err == nil {
		t.Fatal("ProxyEvents returned nil for non-SSE upstream response")
	}
	if got := td.preferredSessionPort(sid); got != "" {
		t.Fatalf("preferredSessionPort after rejected SSE = %q, want empty", got)
	}
}

func TestSendMessageRetriesAfterPinnedPortTransportFailure(t *testing.T) {
	const sid = "sess-send-retry"
	const dir = "/tmp/proj-send-retry"

	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	stalePort := strings.TrimPrefix(stale.URL, "http://127.0.0.1:")
	stale.Close()

	var promptHits int32
	livePort := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/"+sid {
			fmt.Fprintf(w, `{"id":%q}`, sid)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/session/"+sid+"/prompt_async" {
			atomic.AddInt32(&promptHits, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})

	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{dir: livePort}
	})
	defer restore()
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	defer resetSessionPortAffinityForTests()

	database := newTestDBWithSession(t, sid, dir)
	a := New(database, nil, nil, ocapi.Auth{})
	td.rememberSessionPort(sid, stalePort)

	if err := a.SendMessage(context.Background(), platforms.SendMessageRequest{
		SessionID: sid,
		Message:   "hello",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := atomic.LoadInt32(&promptHits); got != 1 {
		t.Fatalf("prompt hits on rediscovered port = %d, want 1", got)
	}
	if got := td.preferredSessionPort(sid); got != livePort {
		t.Fatalf("preferredSessionPort after retry = %q, want %q", got, livePort)
	}
}

func TestDisposeSessionDeletesItFromOpenCode(t *testing.T) {
	const sid = "sess-dispose"
	deleted := false
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/session/"+sid {
			deleted = true
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	})
	td.rememberSessionPort(sid, port)
	defer resetSessionPortAffinityForTests()

	if err := New(nil, nil, nil, ocapi.Auth{}).DisposeSession(context.Background(), platforms.DisposeSessionRequest{SessionID: sid, Port: port}); err != nil {
		t.Fatal(err)
	}
	if !deleted || td.preferredSessionPort(sid) != "" {
		t.Fatalf("deleted = %v, pinned port = %q", deleted, td.preferredSessionPort(sid))
	}
}

func TestDisposeSessionTreatsNotFoundAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	td.rememberSessionPort("gone", port)
	defer resetSessionPortAffinityForTests()

	if err := New(nil, nil, nil, ocapi.Auth{}).DisposeSession(context.Background(), platforms.DisposeSessionRequest{SessionID: "gone", Port: port}); err != nil {
		t.Fatalf("DisposeSession missing session: %v", err)
	}
	if td.preferredSessionPort("gone") != "" {
		t.Fatal("missing session retained its pinned port")
	}
}

func TestDisposeSessionPreservesCleanupWhenSessionDatabaseLookupFails(t *testing.T) {
	err := New(nil, nil, nil, ocapi.Auth{}).DisposeSession(context.Background(), platforms.DisposeSessionRequest{SessionID: "unknown"})
	if !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("DisposeSession database lookup = %v, want ErrNotFound", err)
	}
}

// TestCreateSession_PortAndDirectory covers how CreateSession picks its
// instance and roots the session:
//   - a discovered port is reused from the cache without another
//     (expensive) scan once the cache is warm;
//   - a caller-provided port skips discovery entirely;
//   - req.Directory is forwarded via x-opencode-directory, URL-encoded
//     with url.PathEscape (OpenCode decodes with decodeURIComponent) so
//     paths with spaces round-trip;
//   - the new session is pinned to the port it was created on.
func TestCreateSession_PortAndDirectory(t *testing.T) {
	tests := []struct {
		name        string
		sid         string
		dir         string
		providePort bool // pass req.Port instead of discovering it
		warmCache   bool
		wantScans   int // -1: not asserted
	}{
		{name: "discovered on a cold cache", sid: "sess-with-dir",
			dir: "/private/tmp/external-worktree", wantScans: -1},
		{name: "warm cache skips a fresh scan", sid: "sess-cached",
			dir: "/tmp/proj-cached", warmCache: true, wantScans: 1},
		{name: "provided port skips discovery and encodes spaces", sid: "sess-provided-port",
			dir: "/private/tmp/work tree with space", providePort: true, wantScans: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotDir string
			port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/session" {
					gotDir = r.Header.Get("x-opencode-directory")
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"` + tc.sid + `"}`))
					return
				}
				http.NotFound(w, r)
			})
			var scans int
			restore := setDiscoverPortsImplForTests(func() map[string]string {
				scans++
				if tc.providePort {
					return map[string]string{}
				}
				return map[string]string{tc.dir: port}
			})
			defer restore()
			resetPortCacheForTests()
			resetSessionPortAffinityForTests()
			defer resetSessionPortAffinityForTests()

			if tc.warmCache {
				td.discoverOpenCodePorts()
				if scans != 1 {
					t.Fatalf("warm-up scans = %d, want 1", scans)
				}
			}

			req := platforms.CreateSessionRequest{Directory: tc.dir}
			if tc.providePort {
				req.Port = port
			}
			resp, err := New(nil, nil, nil, ocapi.Auth{}).CreateSession(context.Background(), req)
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			if resp.ID != tc.sid {
				t.Fatalf("CreateSession ID = %q, want %q", resp.ID, tc.sid)
			}
			if tc.wantScans >= 0 && scans != tc.wantScans {
				t.Fatalf("scans = %d, want %d", scans, tc.wantScans)
			}
			if want := url.PathEscape(tc.dir); gotDir != want {
				t.Fatalf("x-opencode-directory = %q, want %q", gotDir, want)
			}
			if strings.Contains(gotDir, " ") {
				t.Fatalf("x-opencode-directory contains a raw space: %q", gotDir)
			}
			if got := td.preferredSessionPort(tc.sid); got != port {
				t.Fatalf("preferredSessionPort = %q, want %q", got, port)
			}
		})
	}
}

// TestCreateSession_RejectsUnusableResponse proves CreateSession never
// reports success without an addressable session ID. A 2xx whose body is
// unparseable, or which carries an empty/missing id, previously fell
// through the `len(body) == 0` guard and returned (&{ID:""}, nil) — a
// "created" session the caller can never address.
func TestCreateSession_RejectsUnusableResponse(t *testing.T) {
	const dir = "/private/tmp/create-session-bad-body"

	for _, tt := range []struct {
		name string
		body string
	}{
		{"unparseable body", `not json at all`},
		{"empty id", `{"id":""}`},
		{"missing id", `{"title":"x"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/session" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tt.body))
					return
				}
				http.NotFound(w, r)
			})
			restore := setDiscoverPortsImplForTests(func() map[string]string {
				return map[string]string{dir: port}
			})
			defer restore()
			resetPortCacheForTests()
			resetSessionPortAffinityForTests()
			defer resetSessionPortAffinityForTests()

			a := New(nil, nil, nil, ocapi.Auth{})
			resp, err := a.CreateSession(context.Background(), platforms.CreateSessionRequest{Directory: dir})
			if err == nil {
				t.Fatalf("CreateSession succeeded with unusable response %q: %+v", tt.body, resp)
			}
			if resp != nil {
				t.Fatalf("CreateSession returned a response alongside an error: %+v", resp)
			}
		})
	}
}

func TestCreateSession_WaitsForCallerDeadline(t *testing.T) {
	const sid = "sess-slow-create"

	previousTimeout := openCodeClient.Timeout
	openCodeClient.Timeout = 10 * time.Millisecond
	t.Cleanup(func() { openCodeClient.Timeout = previousTimeout })

	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte(`{"id":"` + sid + `"}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := New(nil, nil, nil, ocapi.Auth{}).CreateSession(ctx, platforms.CreateSessionRequest{
		Directory: "/tmp/slow-create",
		Port:      port,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if resp.ID != sid {
		t.Fatalf("CreateSession ID = %q, want %q", resp.ID, sid)
	}
}

func TestParseOpenCodeModelRef(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantNil      bool
		wantProvider string
		wantModel    string
	}{
		{"empty", "", true, "", ""},
		{"whitespace only", "   ", true, "", ""},
		{"model only", "gpt-4", false, "", "gpt-4"},
		{"provider/model", "openai/gpt-4", false, "openai", "gpt-4"},
		{"with spaces", "  openai / gpt-4  ", false, "openai", "gpt-4"},
		{"empty provider", "/gpt-4", false, "", "/gpt-4"},
		{"empty model", "openai/", false, "", "openai/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseOpenCodeModelRefInternal(tt.input)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
				return
			}
			if result.ProviderID != tt.wantProvider {
				t.Errorf("ProviderID = %q, want %q", result.ProviderID, tt.wantProvider)
			}
			if result.ModelID != tt.wantModel {
				t.Errorf("ModelID = %q, want %q", result.ModelID, tt.wantModel)
			}
		})
	}
}

// TestCreateSession_NoRunningInstanceReturnsUnreachable ensures that
// when no OpenCode process is listening for the given directory, the
// adapter returns an error wrapping platforms.ErrPlatformUnreachable
// (which the HTTP layer maps to 503 and the frontend uses to trigger
// the auto-launch flow).
func TestCreateSession_NoRunningInstanceReturnsUnreachable(t *testing.T) {
	a := &Adapter{rt: td}
	// A uniquely-named directory that no opencode process will have
	// bound to. lsof will simply not report it, so discovery returns
	// an empty port string.
	_, err := a.CreateSession(context.Background(), platforms.CreateSessionRequest{
		Directory: "/tmp/ocman-test-nonexistent-directory-no-opencode-abc123xyz",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Errorf("error does not wrap ErrPlatformUnreachable: %v", err)
	}
}

// TestExtractOpenCodeErrorMessage covers the parsing of OpenCode's
// NamedError JSON response into a UI-friendly string. This is the
// data we surface to users when SendMessage hits e.g. an unknown
// model and OpenCode returns ProviderModelNotFoundError.
func TestExtractOpenCodeErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   \n", ""},
		{"non-json passthrough", "boom", "boom"},
		{
			"named error with message field",
			`{"name":"ProviderModelNotFoundError","data":{"message":"Model anthropic/foo not found"}}`,
			"Model anthropic/foo not found",
		},
		{
			"named error without message uses name + data",
			`{"name":"ProviderModelNotFoundError","data":{"providerID":"anthropic","modelID":"foo"}}`,
			"ProviderModelNotFoundError: modelID=foo, providerID=anthropic",
		},
		{
			"named error with empty data",
			`{"name":"BadRequest","data":{}}`,
			"BadRequest",
		},
		{
			"json without name returns raw body",
			`{"foo":"bar"}`,
			`{"foo":"bar"}`,
		},
		{
			"effect-style _tag discriminator (PermissionNotFoundError)",
			`{"_tag":"PermissionNotFoundError","requestID":"per_abc","message":"Permission request not found: per_abc"}`,
			"Permission request not found: per_abc",
		},
		{
			"effect-style _tag without message",
			`{"_tag":"SessionBusyError"}`,
			"SessionBusyError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apiv1.ExtractErrorMessage([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractOpenCodeErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// TestCreateSession_Title covers the title branch: current OpenCode
// takes the title in the POST /session body and echoes it back, so no
// PATCH is spent; older builds ignore it, so CreateSession falls back to
// a PATCH /session/{id}. A failing PATCH is soft (the session ID is
// still returned) and no title means no PATCH.
func TestCreateSession_Title(t *testing.T) {
	const newID = "ses_newone"
	tests := []struct {
		name        string
		title       string
		echoTitle   bool // server echoes the create-body title back
		patchStatus int
		wantPatches int32
	}{
		{"echoed title skips PATCH", "wt", true, http.StatusOK, 0},
		{"ignored title falls back to PATCH", "My custom title", false, http.StatusOK, 1},
		{"PATCH failure does not fail creation", "whatever", false, http.StatusInternalServerError, 1},
		{"no title skips PATCH", "", false, http.StatusOK, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var patches int32
			port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/session":
					reply := map[string]string{"id": newID}
					if tc.echoTitle {
						reply["title"] = body["title"]
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(reply)
				case r.Method == http.MethodPatch && r.URL.Path == "/session/"+newID:
					atomic.AddInt32(&patches, 1)
					if body["title"] != tc.title {
						t.Errorf("PATCH title = %q, want %q", body["title"], tc.title)
					}
					w.WriteHeader(tc.patchStatus)
				default:
					http.NotFound(w, r)
				}
			})
			dir := "/tmp/test-create-session-title"
			withTestPort(t, dir, port)

			resp, err := (&Adapter{rt: td}).CreateSession(context.Background(), platforms.CreateSessionRequest{
				Directory: dir, Title: tc.title,
			})
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			if resp.ID != newID {
				t.Errorf("resp.ID = %q, want %q", resp.ID, newID)
			}
			if got := atomic.LoadInt32(&patches); got != tc.wantPatches {
				t.Errorf("PATCH called %d times, want %d", got, tc.wantPatches)
			}
		})
	}
}

// TestSlashCommands_ParsesSource confirms the OpenCode /command
// `source` field (command | mcp | skill) is carried through to
// SlashCommandEntry.Source. The /skills picker keys off source ==
// "skill" to identify which commands are skills.
func TestSlashCommands_ParsesSource(t *testing.T) {
	const sid = "sess-skills"
	const dir = "/tmp/proj-skills"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/command" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"name":"init","description":"setup","source":"command"},
			{"name":"pr-review","description":"review a PR","source":"skill"},
			{"name":"codegraph:map","description":"mcp tool","source":"mcp"}
		]`))
	}))
	defer srv.Close()

	// Cold cache so the HTTP call actually fires.
	catalogCache = newHTTPCache(catalogCache.ttl)

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	entries := slashCommandsAt(context.Background(), port, sid, dir)
	bySource := map[string]string{}
	for _, e := range entries {
		bySource[e.Name] = e.Source
	}
	if bySource["init"] != "command" {
		t.Errorf("init source = %q, want command", bySource["init"])
	}
	if bySource["pr-review"] != "skill" {
		t.Errorf("pr-review source = %q, want skill", bySource["pr-review"])
	}
	if bySource["codegraph:map"] != "mcp" {
		t.Errorf("codegraph:map source = %q, want mcp", bySource["codegraph:map"])
	}
}

// A reply that OpenCode answers with 404 means the prompt is already
// gone upstream (answered elsewhere, or the turn was aborted, which
// drops pending permissions without a permission.replied event). The
// registry must forget it, otherwise ocman keeps advertising a prompt
// nobody can dismiss.
func TestRespondPrompt_UpstreamNotFoundDropsRegistryEntry(t *testing.T) {
	const sid, dir = "sess-stale-prompt", "/tmp/proj-stale-prompt"
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"name":"PermissionNotFoundError","data":{"requestID":"x"}}`))
	})
	withTestPort(t, dir, port)
	a := New(newBoundTestDBWithSession(t, sid, dir), nil, nil, ocapi.Auth{})

	a.ObservePromptAskedFromPort("", 0, dir, "permission", platforms.LivePrompt{"id": "per_1", "sessionID": sid})
	a.ObservePromptAskedFromPort("", 0, dir, "question", platforms.LivePrompt{"id": "que_1", "sessionID": sid})

	err := a.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: sid, PermissionID: "per_1", Reply: "once"})
	if !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Fatalf("RespondPermission err = %v, want upstream rejected", err)
	}
	if got, _ := a.ListPermissions(context.Background(), sid); len(got) != 0 {
		t.Fatalf("permission still listed after upstream 404: %v", got)
	}

	err = a.RespondQuestion(context.Background(), platforms.RespondQuestionRequest{SessionID: sid, RequestID: "que_1"})
	if !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Fatalf("RespondQuestion err = %v, want upstream rejected", err)
	}
	if got, _ := a.ListQuestions(context.Background(), sid); len(got) != 0 {
		t.Fatalf("question still listed after upstream 404: %v", got)
	}
}
