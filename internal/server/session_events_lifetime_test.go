package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// The session-changed reply recovery runs under the server lifetime, so a
// stopped server does no more conversation work.
func TestBroadcastSessionChangedReplyUsesServerLifetime(t *testing.T) {
	stateDB := statetest.Open(t, ModuleSchemas()...)
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{
		PlatformID: "opencode",
		SessionFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session:  &db.Session{ID: id, Status: db.StatusDone},
				Messages: []db.Message{{ID: "m1", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)}},
				Parts:    []db.Part{{ID: "p1", MessageID: "m1", Data: json.RawMessage(`{"type":"text","text":"done"}`)}},
			}, nil
		},
	})
	srv := &Server{stateDB: stateDB, registry: registry, broadcastHub: newBroadcastHub()}
	srv.sessions = sessionsvc.New(registry, sessionsvc.Hooks{})
	key := state.PluginConversationKey{PluginID: "p", AccountID: "a", ThreadID: "t"}
	if _, _, err := stateDB.LinkPluginConversation(t.Context(), key, state.PluginConversationSession{PlatformID: "opencode", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	replied := func() bool {
		ok, err := stateDB.HasPluginConversationReply(t.Context(), key, "s1:m1")
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	srv.lifetimeCtx = stopped
	srv.broadcastSessionChanged("opencode", "s1")
	if replied() {
		t.Fatal("a stopped server recorded a conversation reply")
	}

	srv.lifetimeCtx = t.Context()
	srv.broadcastSessionChanged("opencode", "s1")
	if !replied() {
		t.Fatal("a running server did not record the reply")
	}
}
