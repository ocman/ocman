package pluginsmod

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestSessionPreviewAndDurableCompletion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capability plugins.Capability
		stream     bool
		burst      bool
	}{
		{"opted in", plugins.StreamingSessionCapability, true, false},
		{"finishes between preview ticks", plugins.StreamingSessionCapability, true, true},
		{"capable but not opted in", plugins.StreamingSessionCapability, false, false},
		{"legacy", plugins.FollowupSessionCapability, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capability := tc.capability
			const owner, session, platform = "org.example.im", "session-a", "test"
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			store := openStore(t)
			for _, id := range []string{owner, "org.example.other"} {
				d := plugins.Description{ID: id, Name: "IM", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
					Capabilities: []plugins.Capability{capability}, RequestedGrants: []string{plugins.SessionRunGrant},
					Settings: []plugins.Setting{{Key: "project", Label: "Project", Type: "string", Required: true}}}
				if err := store.DiscoverPlugin(ctx, d, "/"+id, strings.Repeat("a", 64), nil); err != nil {
					t.Fatal(err)
				}
				if err := store.SetPluginEnabled(ctx, id, true, d.RequestedGrants); err != nil {
					t.Fatal(err)
				}
			}
			frames := make(chan string)
			opened := make(chan string, 4)
			p := &previewTestPlatform{followupPlatform: followupPlatform{detail: &platforms.SessionDetail{
				Session: &db.Session{Status: db.StatusDone}, Messages: []db.Message{{ID: "answer", Data: json.RawMessage(`{"role":"assistant"}`)}},
				Parts: []db.Part{{MessageID: "answer", Data: mustPartText(t, "Hello, final text.")}},
			}}, fake: platformtest.Fake{ProxyEventsFn: func(ctx context.Context, id string, w io.Writer, flush func()) error {
				opened <- id
				flush()
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case frame := <-frames:
						if tc.burst {
							_, err := io.WriteString(w, frame)
							return err
						}
						if _, err := fmt.Fprintf(w, "data: %s\n\n", frame); err != nil {
							return err
						}
					}
				}
			}}}
			deps := Deps{Store: store, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }}
			m := NewModule(deps)
			m.Bind(ctx)
			defer func() { cancel(); m.workers.Close() }()
			if err := m.plugins.LinkPluginSession(ctx, platform, session, owner); err != nil {
				t.Fatal(err)
			}
			partials := make(chan plugins.SessionPartial, 4)
			completed := make(chan plugins.SessionCompleted, 1)
			denied := make(chan struct{}, 1)
			handler := plugin.StreamingSessionHandler(func(_ context.Context, _ string, c plugin.SessionCompleted) error {
				completed <- c
				return nil
			}, func(_ context.Context, p plugin.SessionPartial) error { partials <- p; return nil })
			install := func(m *Module) {
				m.sessionOnce.Do(func() {})
				m.sessionBroker = plugins.NewSessionBroker(func(ctx context.Context, id string, use func(plugins.Description, []string, plugins.Config) error) error {
					err := store.WithPluginConversation(ctx, id, use)
					if err != nil {
						denied <- struct{}{}
					}
					return err
				}, nil, func(ctx context.Context, id string, call plugins.Call) (<-chan plugins.Reply, error) {
					if id != owner || call.Version != capability.Version {
						t.Errorf("unexpected recipient or version: %q %+v", id, call.Version)
					}
					if capability.Version.Minor == 5 && strings.Contains(string(call.Params), `"turnId"`) {
						t.Errorf("legacy callback payload changed: %s", call.Params)
					}
					value, err := handler(ctx, call, nil)
					replies := make(chan plugins.Reply, 1)
					replies <- plugins.Reply{Message: plugins.Envelope{Type: plugins.TypeResult, Result: &plugins.Result{Value: value}}, Err: err}
					return replies, nil
				})
			}
			install(m)
			var turnID string
			// The other plugin and the same ID on another platform own neither binding.
			m.watchSessionPreview(ctx, "org.example.other", platform, session, "", true)
			m.watchSessionPreview(ctx, owner, "different-platform", session, "", true)
			if tc.burst {
				stop := m.watchSessionPreview(ctx, owner, platform, session, "", true)
				defer stop()
				<-opened
				frames <- `data: {"type":"message.updated","properties":{"info":{"id":"answer","sessionID":"session-a","role":"assistant"}}}` + "\n\n" +
					`data: {"type":"message.part.updated","properties":{"part":{"id":"visible","messageID":"answer","sessionID":"session-a","type":"text","text":"Hello"}}}` + "\n\n" +
					`data: {"type":"session.idle","properties":{"sessionID":"session-a"}}` + "\n\n"
				p := receivePreview(t, ctx, partials)
				if p.Text != "Hello" {
					t.Fatalf("wrong preview: %+v", p)
				}
				turnID = p.TurnID
			} else if tc.stream && capability == plugins.StreamingSessionCapability {
				stop := m.watchSessionPreview(ctx, owner, platform, session, "", true)
				defer stop()
				if receivePreview(t, ctx, opened) != session {
					t.Fatal("wrong stream opened")
				}
				emit := func(frame string) {
					select {
					case frames <- frame:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				emit(`{"type":"message.updated","properties":{"info":{"id":"answer","sessionID":"session-a","role":"assistant"}}}`)
				emit(`{"type":"message.part.updated","properties":{"part":{"id":"private","messageID":"answer","sessionID":"session-a","type":"reasoning","text":"secret"}}}`)
				emit(`{"type":"message.part.delta","properties":{"partID":"private","messageID":"answer","sessionID":"session-a","field":"text","delta":"secret"}}`)
				emit(`{"type":"message.part.updated","properties":{"part":{"id":"tool","messageID":"answer","sessionID":"session-a","type":"tool","text":"tool output"}}}`)
				emit(`{"type":"message.part.updated","properties":{"part":{"id":"foreign","messageID":"answer","sessionID":"other","type":"text","text":"foreign session"}}}`)
				emit(`{"type":"message.part.updated","properties":{"part":{"id":"visible","messageID":"answer","sessionID":"session-a","type":"text","text":"Hello"}}}`)
				first := receivePreview(t, ctx, partials)
				emit(`{"type":"message.part.delta","properties":{"partID":"visible","messageID":"answer","sessionID":"session-a","field":"text","delta":", world"}}`)
				second := receivePreview(t, ctx, partials)
				if first.SessionID != session || first.TurnID == "" || first.Text != "Hello" || second.Text != "Hello, world" || first.TurnID != second.TurnID || second.SessionID != session {
					t.Fatalf("unexpected previews: %+v %+v", first, second)
				}
				turnID = first.TurnID
				if err := store.SetPluginEnabled(ctx, owner, false, nil); err != nil {
					t.Fatal(err)
				}
				emit(`{"type":"message.part.delta","properties":{"partID":"visible","messageID":"answer","sessionID":"session-a","field":"text","delta":" revoked"}}`)
				receivePreview(t, ctx, denied)
				stop()
				if err := store.SetPluginEnabled(ctx, owner, true, []string{plugins.SessionRunGrant}); err != nil {
					t.Fatal(err)
				}
			} else {
				m.watchSessionPreview(ctx, owner, platform, session, "", tc.stream)
			}
			m.workers.Close()
			if len(opened) != 0 || len(partials) != 0 {
				t.Fatal("unexpected stream or unauthorized preview")
			}
			m.ReplyToSession(ctx, platform, session)
			reconnected := NewModule(deps)
			install(reconnected)
			rows, err := reconnected.plugins.ClaimPluginSessionCompletions(ctx, 4)
			if err != nil || len(rows) != 1 {
				t.Fatalf("expected one durable completion: %v, %v", rows, err)
			}
			reconnected.deliverSessionCompletion(ctx, rows[0])
			got := receivePreview(t, ctx, completed)
			if got.SessionID != session || (turnID != "" && got.TurnID != turnID) ||
				(got.TurnID != "") != (capability == plugins.StreamingSessionCapability) || got.Text != "Hello, final text." {
				t.Fatalf("wrong final: %+v", got)
			}
		})
	}
}

type previewTestPlatform struct {
	followupPlatform
	fake platformtest.Fake
}

func (p *previewTestPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Events: true}
}

func (p *previewTestPlatform) ProxyEvents(ctx context.Context, id string, w io.Writer, flush func()) error {
	return p.fake.ProxyEvents(ctx, id, w, flush)
}

func receivePreview[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		var zero T
		return zero
	}
}
