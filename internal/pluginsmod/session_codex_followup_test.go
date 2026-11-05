package pluginsmod

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexprovider "github.com/ocman/ocman/internal/platforms/codex/provider"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type storedCodexFollowup struct{ *followupPlatform }

func (p *storedCodexFollowup) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return codexprovider.StoredLifecycle(&db.HistorySnapshot{InferredStatus: p.detail.Session.Status, Messages: p.detail.Messages}), nil
}

func TestSessionFollowupAcceptsStoredCodexCompletionReceipt(t *testing.T) {
	m, p, after, sent := followupFixture(t)
	stored := &storedCodexFollowup{p}
	m.deps.Adapter = func(context.Context, string, string) (platforms.Platform, bool) { return stored, true }
	id, err := m.sendSession(t.Context(), "plugin-a", plugins.SessionSendParams{After: after, Prompt: "Continue the completed Codex reply"}, "codex-followup")
	if err != nil || id != "session-a" || len(*sent) != 1 {
		t.Fatalf("stored Codex receipt rejected: id=%s sent=%d err=%v", id, len(*sent), err)
	}
}
