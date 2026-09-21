package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/plugins"
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

func TestConversationReconciliationRecoversMissedIdle(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	f.s.reconcileConversationReplies(t.Context())
	deadline := time.Now().Add(10 * time.Second)
	for f.replies() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.replies() == "" {
		t.Fatal("settled conversation was not recovered without an idle edge")
	}
}

func TestConversationConfiguredModelAndAgentAreQueued(t *testing.T) {
	f := newConversationFixture(t)
	f.setBusy(true)
	config := plugins.ConversationConfig{
		Project: f.project,
		Values: map[string]json.RawMessage{
			"agent": json.RawMessage(`"build"`),
			"model": json.RawMessage(`"openai/gpt-5.6"`),
		},
	}
	if err := f.s.startConversation(context.Background(), conversationPluginDescription().ID, config, conversationMessage("use the configured model")); err != nil {
		t.Fatal(err)
	}
	queued, err := f.s.queueSvc().List(t.Context(), "opencode", "ses-chat")
	if err != nil || len(queued) != 1 {
		t.Fatalf("queued %v: %v", queued, err)
	}
	if queued[0].Agent != "build" || queued[0].Model != "openai/gpt-5.6" {
		t.Fatalf("agent/model %q/%q", queued[0].Agent, queued[0].Model)
	}
}
