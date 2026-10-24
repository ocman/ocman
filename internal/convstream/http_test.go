package convstream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type streamFixture struct {
	srv    *httptest.Server
	frames chan string
	authed atomic.Int32
	traced atomic.Int32
}

func newStreamFixture(t *testing.T, fail error) *streamFixture {
	t.Helper()
	fx := &streamFixture{frames: make(chan string, 16)}
	fp := &fakePlatform{id: "opencode"}
	m := NewModule(Deps{
		WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, platforms.Platform)) {
			fn(w, r, r.PathValue("id"), fp)
		},
		Proxy: func(ctx context.Context, _ string, _ platforms.Platform, raw, _ io.Writer, _ func()) error {
			if fail != nil {
				return fail
			}
			_, _ = io.WriteString(raw, v1("server.connected", map[string]any{}))
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case f := <-fx.frames:
					if _, err := io.WriteString(raw, f); err != nil {
						return err
					}
				}
			}
		},
		Keepalive: func() time.Duration { return 20 * time.Millisecond },
		Trace: func(h http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if fx.authed.Load() == 0 {
					panic("traced before the login guard")
				}
				fx.traced.Add(1)
				h(w, r)
			}
		},
	})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: mux, Auth: func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fx.authed.Add(1); h(w, r) }
	}})
	fx.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		fx.srv.CloseClientConnections()
		fx.srv.Close()
		m.Close()
	})
	return fx
}

type sseClient struct {
	resp *http.Response
	r    *bufio.Reader
}

func (fx *streamFixture) open(t *testing.T, query string, header http.Header) *sseClient {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, fx.srv.URL+"/api/session/ses_root/stream"+query, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &sseClient{resp: resp, r: bufio.NewReader(resp.Body)}
}

// next returns the next frame's id, event name and envelope (when oce).
func (c *sseClient) next(t *testing.T) (id, event string, env convevents.Envelope) {
	t.Helper()
	var block strings.Builder
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (after %q)", err, block.String())
		}
		if line == "\n" {
			break
		}
		block.WriteString(line)
	}
	for _, line := range strings.Split(block.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		}
	}
	if event == "oce" {
		env = decode(t, []byte(block.String()))
	}
	return id, event, env
}

// nextOCE skips keepalives.
func (c *sseClient) nextOCE(t *testing.T) (string, convevents.Envelope) {
	t.Helper()
	for {
		id, event, env := c.next(t)
		if event == "oce" {
			return id, env
		}
	}
}

func TestStreamEndpointServesHelloLiveAndResume(t *testing.T) {
	fx := newStreamFixture(t, nil)
	c := fx.open(t, "", nil)
	if c.resp.StatusCode != http.StatusOK || c.resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content type %q", c.resp.StatusCode, c.resp.Header.Get("Content-Type"))
	}
	if fx.authed.Load() != 1 || fx.traced.Load() != 1 {
		t.Fatal("the stream route must be behind the auth guard")
	}
	id, hello := c.nextOCE(t)
	if hello.Event.EventType() != convevents.TypeStreamHello || id != hello.Cursor() {
		t.Fatalf("first frame %s id %q", hello.Event.EventType(), id)
	}
	if ov := hello.Event.(convevents.StreamHello).Overlay; ov == nil || !ov.LiveConnection {
		t.Fatalf("cold hello overlay = %+v", ov)
	}

	fx.frames <- v1("session.status", map[string]any{"sessionID": "ses_root", "status": map[string]any{"type": "busy"}})
	var cursor string
	for _, want := range []string{convevents.TypeTurnStarted, convevents.TypeSessionStatus} {
		var env convevents.Envelope
		cursor, env = c.nextOCE(t)
		if env.Event.EventType() != want || cursor != env.Cursor() {
			t.Fatalf("got %s id %q, want %s", env.Event.EventType(), cursor, want)
		}
	}
	if _, event, _ := c.next(t); event != "ocman.keepalive" {
		t.Fatalf("idle stream sent %q, want a keepalive", event)
	}

	fx.frames <- v1("session.diff", map[string]any{"sessionID": "ses_root"})
	_, dirty := c.nextOCE(t)
	if dirty.Event.EventType() != convevents.TypeChangesDirty {
		t.Fatalf("got %s", dirty.Event.EventType())
	}

	for _, resume := range []*sseClient{fx.open(t, "?after="+cursor, nil), fx.open(t, "", http.Header{"Last-Event-ID": {cursor}})} {
		id, hello := resume.nextOCE(t)
		if !hello.Event.(convevents.StreamHello).Resumed || id != cursor {
			t.Fatalf("resume hello = %+v id %q", hello, id)
		}
		if _, replay := resume.nextOCE(t); replay.Seq != dirty.Seq {
			t.Fatalf("replay = %+v, want seq %d", replay, dirty.Seq)
		}
	}
	stale := fx.open(t, fmt.Sprintf("?after=%s:%d", "nope", 1), nil)
	if _, hello := stale.nextOCE(t); hello.Event.(convevents.StreamHello).Resumed {
		t.Fatal("a foreign epoch must not resume")
	}
}

func TestStreamEndpointFailsBeforeHeaders(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		body string
	}{
		{fmt.Errorf("no instance: %w", platforms.ErrPlatformUnreachable), http.StatusServiceUnavailable, "no running platform instance"},
		{platforms.ErrUnsupported, http.StatusNotImplemented, "not supported"},
		{fmt.Errorf("probe: %w", platforms.ErrPlatformUnauthenticated), http.StatusBadGateway, "platform authentication failed"},
		{errors.New("broken"), http.StatusBadGateway, "session event source failed"},
	} {
		fx := newStreamFixture(t, tc.err)
		c := fx.open(t, "", nil)
		if c.resp.StatusCode != tc.code || strings.HasPrefix(c.resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%v: status %d content type %q", tc.err, c.resp.StatusCode, c.resp.Header.Get("Content-Type"))
		}
		if body, _ := io.ReadAll(c.resp.Body); !strings.Contains(string(body), tc.body) {
			t.Fatalf("%v: body %q, want %q", tc.err, body, tc.body)
		}
	}
}

func TestStreamEndpointWritesResetThenNewHello(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	m := &Module{hub: h, d: Deps{Keepalive: func() time.Duration { return time.Hour }}}
	m.d.WithSessionAdapter = func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, platforms.Platform)) {
		fn(w, r, testKey.SessionID, &fakePlatform{id: platforms.ID(testKey.Platform)})
	}
	// The hub reuses the attached source; prime it with the fake.
	primer := subscribe(t, h, src, "")
	nextSink(t, src)
	srv := httptest.NewServer(http.HandlerFunc(m.HandleStream))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	c := &sseClient{resp: resp, r: bufio.NewReader(resp.Body)}
	_, first := c.nextOCE(t)
	primer.Close()
	src.end <- errBoom
	if _, e := c.nextOCE(t); e.Event.EventType() != convevents.TypeStreamError {
		t.Fatalf("got %s, want stream.error", e.Event.EventType())
	}
	id, reset := c.nextOCE(t)
	if reset.Event.(convevents.StreamReset).Reason != convevents.ResetSourceRestarted || id != "" || reset.Epoch != first.Epoch {
		t.Fatalf("reset = %+v id %q", reset, id)
	}
	_, hello := c.nextOCE(t)
	if hello.Event.EventType() != convevents.TypeStreamHello || hello.Epoch == first.Epoch {
		t.Fatalf("after reset got %+v", hello)
	}
}
