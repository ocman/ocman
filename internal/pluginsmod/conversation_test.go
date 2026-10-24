package pluginsmod

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestConversationReplyReady(t *testing.T) {
	for status, want := range map[db.SessionStatus]bool{
		db.StatusBusy: false, db.StatusLimited: false, db.StatusInterrupted: false,
		db.StatusWaiting: true, db.StatusDone: true, db.StatusError: true,
	} {
		if got := conversationReplyReady(status); got != want {
			t.Fatalf("status %q: got %v, want %v", status, got, want)
		}
	}
}

type stubHost struct {
	hostsvc.Host
	ensured *string
}

func (h stubHost) EnsureRuntime(_ context.Context, platform, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	if h.ensured != nil {
		*h.ensured = platform
	}
	return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:1234", RepoRoot: dir}, nil
}

// localPlatforms resolves platforms as the server does for this machine:
// empty means opencode, and only the listed ones are registered.
func localPlatforms(ids ...string) (func(context.Context, hostsvc.Host, string, string) (string, string, error), func(string) (platforms.Platform, bool)) {
	create := func(_ context.Context, _ hostsvc.Host, requested, model string) (string, string, error) {
		requested, err := platforms.PlatformForModel(requested, model)
		if err != nil {
			return "", "", err
		}
		if requested == "" {
			requested = "opencode"
		}
		return requested, requested, nil
	}
	lookup := func(id string) (platforms.Platform, bool) {
		for _, known := range ids {
			if id == known {
				return nil, true
			}
		}
		return nil, false
	}
	return create, lookup
}

type stubSessions struct {
	created []platforms.CreateSessionRequest
}

func (s *stubSessions) Create(_ context.Context, platformID string, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	s.created = append(s.created, req)
	return &platforms.CreateSessionResponse{ID: "ses-chat"}, nil
}

func (s *stubSessions) CreateConfigured(ctx context.Context, platformID string, req platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	return s.Create(ctx, platformID, req)
}

// The first message creates the session on this machine (never an inferred
// owner) and queues, not sends, with the configured agent and model.
func TestConversationConfiguredModelAndAgentAreQueued(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	type enqueued struct {
		platform string
		force    bool
		req      platforms.SendMessageRequest
	}
	var got []enqueued
	sessions := &stubSessions{}
	create, lookup := localPlatforms("opencode")
	m := NewModule(Deps{
		Store: store, Local: func() hostsvc.Host { return stubHost{} }, Sessions: sessions,
		CreatePlatform: create, Platform: lookup,
		Enqueue: func(_ context.Context, platform string, force bool, req platforms.SendMessageRequest) error {
			got = append(got, enqueued{platform, force, req})
			return nil
		},
	})
	project := t.TempDir()
	config := plugins.Config{Project: project, Values: map[string]json.RawMessage{
		"agent": json.RawMessage(`"build"`), "model": json.RawMessage(`"openai/gpt-5.6"`),
	}}
	message := plugins.ConversationMessage{AccountID: "T1", ThreadID: "C1:1", EventID: "Ev1", Text: "use the configured model"}
	if err := m.startConversation(t.Context(), "org.example.chatops", config, message); err != nil {
		t.Fatal(err)
	}
	if len(sessions.created) != 1 || sessions.created[0].Directory != filepath.Clean(project) {
		t.Fatalf("created %+v", sessions.created)
	}
	want := enqueued{"opencode", false, platforms.SendMessageRequest{SessionID: "ses-chat", Message: message.Text, Agent: "build", Model: "openai/gpt-5.6"}}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("enqueued %+v, want %+v", got, want)
	}
	// A redelivered event is dropped by its receipt: no second prompt.
	if err := m.startConversation(t.Context(), "org.example.chatops", config, message); err != nil || len(got) != 1 {
		t.Fatalf("redelivery: %v, %d prompts", err, len(got))
	}
}

// The installation's platform value picks the session's platform; an invalid
// or unregistered one creates nothing.
func TestConversationPlatformValue(t *testing.T) {
	for raw, want := range map[string]string{`"claudecode"`: "claudecode", `17`: "", `"unknown"`: ""} {
		t.Run(raw, func(t *testing.T) {
			store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), Schema)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			var ensured string
			create, lookup := localPlatforms("opencode", "claudecode")
			m := NewModule(Deps{
				Store: store, Local: func() hostsvc.Host { return stubHost{ensured: &ensured} }, Sessions: &stubSessions{},
				CreatePlatform: create, Platform: lookup,
				Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error { return nil },
			})
			key := state.PluginConversationKey{PluginID: "p", AccountID: "a", ThreadID: "t"}
			_ = m.deliverConversationPrompt(t.Context(), key, "/repo", map[string]json.RawMessage{"platform": json.RawMessage(raw)}, plugins.ConversationMessage{Text: "hi"})
			linked, found, err := store.GetPluginConversation(t.Context(), key)
			if err != nil || found != (want != "") || linked.PlatformID != want || ensured != want {
				t.Fatalf("linked = %+v, found %v, runtime %q, %v", linked, found, ensured, err)
			}
		})
	}
}
