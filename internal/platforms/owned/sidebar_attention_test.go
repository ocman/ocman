package owned

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

func TestCompactSidebarPreservesAttentionTimestamps(t *testing.T) {
	a, _, id := storedFixture(t)
	_, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: id}, CreatedAt: 1, UpdatedAt: 400, ReadFormat: statev2.ConversationReadFormat}, ExpectedRevision: 1, Messages: []statev2.Message{
		{ID: "u", Role: "user", ConversationVisible: true, CreatedAt: 300},
		{ID: "a", Role: "assistant", ConversationVisible: true, CreatedAt: 310, CompletedAt: 350, Position: 1, Finish: "stop"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.store.FilePath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	core, err := state.Open(path, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if _, err := core.RecordSessionHalt(t.Context(), id, "permission", "p", 375); err != nil {
		t.Fatal(err)
	}
	rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
	defer func() {
		if a.sidebarReader != nil {
			a.sidebarReader.Close()
		}
	}()
	if err != nil || len(rows) != 1 {
		t.Fatalf("sidebar: %+v %v", rows, err)
	}
	r := rows[0]
	if r.LastTurnCompletedAt != 350 || r.LastUserPromptAt != 300 || r.LastHaltAt != 375 {
		t.Fatalf("compact attention timestamps missing: %+v", r)
	}
}
