package pluginsmod

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	ocv2 "github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// The preview follows the OpenCode v2 provider path: the plugin stream is open
// before the prompt, and v2 publishes step.started together with the step's
// first text, so the translator reads a stored message whose text has started.
// Visible text must reach the plugin while the turn is still running.
func TestSessionPreviewStreamsOpenCodeV2TextBeforeIdle(t *testing.T) {
	const owner, session = "org.example.im", "session-a"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	store := openStore(t)
	d := plugins.Description{ID: owner, Name: "IM", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Capabilities: []plugins.Capability{plugins.StreamingSessionCapability}, RequestedGrants: []string{plugins.SessionRunGrant},
		Settings: []plugins.Setting{{Key: "project", Label: "Project", Type: "string", Required: true}}}
	if err := store.DiscoverPlugin(ctx, d, "/"+owner, strings.Repeat("a", 64), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPluginEnabled(ctx, owner, true, d.RequestedGrants); err != nil {
		t.Fatal(err)
	}

	// The turn stays active until the test releases its end.
	turnEnded := make(chan struct{})
	v2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		event := func(typ, data string) {
			fmt.Fprintf(w, "data: {\"id\":\"evt\",\"type\":%q,\"created\":100,\"data\":{\"sessionID\":%q,\"assistantMessageID\":\"answer\"%s}}\n\n", typ, session, data)
			w.(http.Flusher).Flush()
		}
		if r.URL.Path != "/api/event" {
			w.Header().Set("Content-Type", "application/json")
		}
		switch r.URL.Path {
		case "/api/info":
			_, _ = io.WriteString(w, `{"version":"2.2.7"}`)
		case "/api/session/active":
			_, _ = io.WriteString(w, `{"data":{}}`)
		case "/api/debug/location":
			_, _ = io.WriteString(w, `[]`)
		case "/api/session/" + session + "/message/answer":
			_, _ = io.WriteString(w, `{"data":{"id":"answer","type":"assistant","time":{"created":100},"content":[{"type":"text","text":""}]}}`)
		case "/api/event":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"evt\",\"type\":\"server.connected\",\"data\":{}}\n\n")
			w.(http.Flusher).Flush()
			event("session.execution.started", "")
			event("session.step.started", `,"started":100`)
			event("session.text.started", `,"ordinal":0`)
			event("session.reasoning.delta", `,"ordinal":0,"delta":"private"`)
			event("session.text.delta", `,"ordinal":0,"delta":"Hel"`)
			event("session.text.delta", `,"ordinal":0,"delta":"lo"`)
			select {
			case <-turnEnded:
			case <-r.Context().Done():
				return
			}
			event("session.text.ended", `,"ordinal":0,"text":"Hello"`)
			event("session.step.ended", `,"finish":"stop"`)
			event("session.execution.succeeded", "")
		default:
			http.NotFound(w, r)
		}
	}))
	defer v2.Close()
	defer close(turnEnded)

	client := &http.Client{Transport: ocv2.WrapIsolated(nil)}
	p := &previewTestPlatform{followupPlatform: followupPlatform{}, fake: platformtest.Fake{
		ProxyEventsFn: func(ctx context.Context, _ string, w io.Writer, flush func()) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, v2.URL+"/event?directory=/project", nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			buf := make([]byte, 4096)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						return werr
					}
					flush()
				}
				if err != nil {
					return err
				}
			}
		},
	}}
	m := NewModule(Deps{Store: store, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }})
	m.Bind(ctx)
	defer func() { cancel(); m.workers.Close() }()
	if err := m.plugins.LinkPluginSession(ctx, "test", session, owner); err != nil {
		t.Fatal(err)
	}
	partials := make(chan plugins.SessionPartial, 8)
	handler := plugin.StreamingSessionHandler(func(context.Context, string, plugin.SessionCompleted) error { return nil },
		func(_ context.Context, p plugin.SessionPartial) error { partials <- p; return nil })
	m.sessionOnce.Do(func() {})
	m.sessionBroker = plugins.NewSessionBroker(store.WithPluginConversation, nil, func(ctx context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
		value, err := handler(ctx, call, nil)
		replies := make(chan plugins.Reply, 1)
		replies <- plugins.Reply{Message: plugins.Envelope{Type: plugins.TypeResult, Result: &plugins.Result{Value: value}}, Err: err}
		return replies, nil
	})

	stop := m.watchSessionPreview(ctx, owner, "test", session, "", true)
	defer stop()
	for {
		got := receivePreview(t, ctx, partials)
		if strings.Contains(got.Text, "private") {
			t.Fatalf("reasoning reached the preview: %+v", got)
		}
		if got.SessionID == session && got.Text == "Hello" {
			return // delivered while the turn is still active
		}
	}
}
