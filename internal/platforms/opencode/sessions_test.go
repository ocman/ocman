package opencode

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionsKeepOldPromptsUntilResolved(t *testing.T) {
	for _, kind := range []string{"permission", "question"} {
		t.Run(kind, func(t *testing.T) {
			const dir, sid = "/repo", "old-prompt"
			InvalidateSessionsCache()
			t.Cleanup(InvalidateSessionsCache)
			a := New(newTestDBWithSessions(t, []testSession{
				{id: sid, directory: dir, updated: 1},
				{id: "old-quiet", directory: dir, updated: 1},
				{id: "recent", directory: dir, updated: 2000},
				{id: "other-project", directory: "/other", updated: 1},
			}), nil)
			a.ObservePromptAsked("", dir, kind, platforms.LivePrompt{"id": "prompt", "sessionID": sid})
			a.ObservePromptAsked("", "/other", kind, platforms.LivePrompt{"id": "other", "sessionID": "other-project"})
			for _, load := range []string{"fresh", "reconciliation"} {
				rows, err := a.Sessions(t.Context(), dir, 1000)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 2 {
					t.Fatalf("%s returned %+v; want recent and old prompt", load, rows)
				}
				found := false
				for _, row := range rows {
					if row.ID == sid {
						found = true
						if row.TimeUpdated != 1 || !row.PendingPermission && !row.PendingQuestion {
							t.Fatalf("prompt lost its flags or timestamp: %+v", row)
						}
					}
				}
				if !found {
					t.Fatalf("%s omitted old prompt: %+v", load, rows)
				}
			}
			a.ObservePromptResolved(dir, kind, sid, "prompt")
			rows, err := a.Sessions(t.Context(), dir, 1000)
			if err != nil || len(rows) != 1 || rows[0].ID != "recent" {
				t.Fatalf("resolved prompt did not restore time filtering: %+v, %v", rows, err)
			}
		})
	}
}
