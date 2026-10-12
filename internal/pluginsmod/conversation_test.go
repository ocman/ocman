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
		db.StatusBusy: false, db.StatusInterrupted: false,
		db.StatusWaiting: true, db.StatusDone: true, db.StatusError: true,
	} {
		if got := conversationReplyReady(status); got != want {
			t.Fatalf("status %q: got %v, want %v", status, got, want)
		}
	}
}

type stubHost struct{ hostsvc.Host }

func (stubHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:1234", RepoRoot: req.ProjectDir}, nil
}

type stubSessions struct {
	created []platforms.CreateSessionRequest
}

func (s *stubSessions) Create(_ context.Context, platformID string, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	s.created = append(s.created, req)
	return &platforms.CreateSessionResponse{ID: "ses-chat"}, nil
}

// The first message creates the session on this machine (never an inferred
// owner) and queues, not sends, with the configured agent and model.
func TestConversationConfiguredModelAndAgentAreQueued(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
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
	m := NewModule(Deps{
		Store: store, Local: func() hostsvc.Host { return stubHost{} }, Sessions: sessions,
		Enqueue: func(_ context.Context, platform string, force bool, req platforms.SendMessageRequest) error {
			got = append(got, enqueued{platform, force, req})
			return nil
		},
	})
	project := t.TempDir()
	config := plugins.ConversationConfig{Project: project, Values: map[string]json.RawMessage{
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
