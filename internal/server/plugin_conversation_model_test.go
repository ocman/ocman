package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

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
