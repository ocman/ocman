package pluginsmod

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type legacyPluginPlatform struct{ *conversationLifecyclePlatform }

func (*legacyPluginPlatform) PluginSessionReference(context.Context, string) (string, error) {
	return "native", nil
}

func TestCanonicalPluginReceiptsKeepLegacyCompletionCorrelations(t *testing.T) {
	core, err := state.Open(filepath.Join(t.TempDir(), "state.db"), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	m := NewModule(Deps{Store: core})
	key := state.PluginConversationKey{PluginID: "plugin", AccountID: "account", ThreadID: "thread"}
	if _, err := core.AppendPluginConversationReply(t.Context(), key, "native:message", "answer"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.plugins.AppendPluginSessionCompletion(t.Context(), "plugin", "test", "canonical", "native:message", "answer"); err != nil {
		t.Fatal(err)
	}
	p := &legacyPluginPlatform{conversationLifecyclePlatform: &conversationLifecyclePlatform{lifecycle: platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageRole: "assistant", LatestMessageID: "message"}}}
	if skip, err := m.conversationReplyAlreadyRecorded(t.Context(), p, key, "canonical"); err != nil || !skip {
		t.Errorf("canonical replay lost conversation receipt: %v %v", skip, err)
	}
	if skip, err := m.sessionCompletionAlreadyRecorded(t.Context(), p, "plugin", "canonical"); err != nil || !skip {
		t.Errorf("canonical replay lost session receipt: %v %v", skip, err)
	}
}
