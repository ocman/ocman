package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/convstream"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// A source that fails before its first byte answers with a real status, never
// an empty 200 event stream: EventSource would reconnect in a tight loop and
// starve the browser's connection pool.
func TestConversationStream_FailsBeforeFirstByte(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("none: %w", platforms.ErrPlatformUnreachable), http.StatusServiceUnavailable},
		{fmt.Errorf("login: %w", ocapi.ErrAuthentication), http.StatusBadGateway},
	} {
		srv, reg := newSessionsTestServer(t)
		reg.Register(&fakePlatform{id: "fake", sessions: []db.Session{{ID: "s1", Platform: "fake"}},
			proxyEventsFn: func(context.Context, string, io.Writer, func()) error { return tc.err }})
		t.Cleanup(srv.convStreamModule().Close)
		mux, err := srv.routes()
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/s1/stream?platform=fake", nil))
		if rec.Code != tc.code || strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%v: status = %d, content type %q", tc.err, rec.Code, rec.Header().Get("Content-Type"))
		}
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/missing/stream", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown session status = %d", rec.Code)
		}
		// The legacy per-session event route is gone.
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/s1/events?platform=fake", nil))
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") || rec.Code == http.StatusOK {
			t.Fatalf("/events status = %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// The stream runs the owner's pipeline: pending prompts seed the
// hello overlay and live v1 frames arrive mapped to OCE.
func TestConversationStream_LocalOwnerFramesAreMapped(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	live := make(chan string, 4)
	pending := platforms.LivePrompt{"id": "per_1", "sessionID": "s1", "permission": "bash", "patterns": []any{"ls"}}
	reg.Register(&fakePlatform{id: "fake", sessions: []db.Session{{ID: "s1", Platform: "fake"}},
		listPermissionsFn: func(string) ([]platforms.LivePrompt, error) { return []platforms.LivePrompt{pending}, nil },
		proxyEventsFn: func(ctx context.Context, _ string, w io.Writer, _ func()) error {
			_, _ = io.WriteString(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case f := <-live:
					_, _ = io.WriteString(w, f)
				}
			}
		}})
	t.Cleanup(srv.convStreamModule().Close)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(func() { hs.CloseClientConnections(); hs.Close() })
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, hs.URL+"/api/session/s1/stream?platform=fake", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status = %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	r := bufio.NewReader(resp.Body)
	hello := readOCE(t, r)
	ov := hello.Event.(convevents.StreamHello).Overlay
	if ov == nil || len(ov.Prompts) != 1 || ov.Prompts[0].ID != "per_1" || ov.Status.Status != db.StatusWaiting {
		t.Fatalf("hello overlay = %+v", ov)
	}
	live <- "data: {\"type\":\"permission.replied\",\"properties\":{\"sessionID\":\"s1\",\"requestID\":\"per_1\",\"reply\":\"once\"}}\n\n"
	live <- "data: {\"type\":\"ocman.queue.changed\",\"properties\":{\"sessionID\":\"s1\"}}\n\n"
	// The user's reply also reaches the stream as auto-approve's synthetic
	// approval through its sink, mapped to the notice records.
	seen := map[string]bool{}
	for !seen[convevents.TypeQueueChanged] || !seen["notice"] {
		env := readOCE(t, r)
		seen[env.Event.EventType()] = true
		switch e := env.Event.(type) {
		case convevents.MessageUpserted:
			seen["notice"] = seen["notice"] || e.Message.ID == "ocman-notice-per_1"
		case convevents.QueueChanged:
			if e.Messages == nil {
				t.Fatal("queue.changed without the read list")
			}
		case convevents.SessionStatusEvent:
			seen["busy"] = e.Status != db.StatusWaiting
		}
	}
	if !seen[convevents.TypePromptResolved] || !seen["busy"] {
		t.Fatalf("seen = %v", seen)
	}
}

// Remote owners already ran their judge and sinks; the hub only forwards
// their bytes.
func TestConversationSource_RemoteBridgeForwardsBytes(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	var listed bool
	remote := &fakePlatform{id: "r-box:opencode",
		listPermissionsFn: func(string) ([]platforms.LivePrompt, error) { listed = true; return nil, nil },
		proxyEventsFn: func(_ context.Context, _ string, w io.Writer, _ func()) error {
			_, err := io.WriteString(w, "data: {}\n\n")
			return err
		}}
	var raw strings.Builder
	if err := srv.proxyConversationSource(t.Context(), "s1", remote, &raw, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	if raw.String() != "data: {}\n\n" || listed {
		t.Fatalf("raw = %q, listed = %v", raw.String(), listed)
	}
	if srv.isDescendantSession(t.Context(), convstream.Key{Platform: "missing", SessionID: "root"}, "child") {
		t.Fatal("no session database means no known descendants")
	}
}

func readOCE(t *testing.T, r *bufio.Reader) convevents.Envelope {
	t.Helper()
	for {
		var data string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if line == "\n" {
				break
			}
			if d, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "data: "); ok {
				data = d
			}
		}
		if data == "" || data == "{}" {
			continue
		}
		var env convevents.Envelope
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
		return env
	}
}
