package pluginsmod

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestConversationNoticeGuardsWithoutStore(t *testing.T) {
	m := NewModule(Deps{SessionURL: func(context.Context, string, string) (string, error) { return "", nil }})
	if got := m.conversationNoticeFor(t.Context(), "no-such-kind", "opencode", "s"); got != "" {
		t.Fatalf("unknown kind = %q", got)
	}
	m.appendConversationNotice(t.Context(), state.PluginConversationKey{}, "op", "text")
	m.notifyConversationSession(t.Context(), "opencode", "s", "op", "kind")
	m.deliverSessionCompletion(t.Context(), PluginSessionCompletion{SessionID: "ocref_123e4567-e89b-12d3-a456-426614174000"})
}
