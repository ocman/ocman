package opencode

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestSessionHaltsSurviveResolutionReplayAndAdapterRestart(t *testing.T) {
	InvalidateSessionsCache()
	parent, child := "parent", "child"
	database := newTestDBWithSessions(t, []testSession{
		{id: parent, directory: "/repo"},
		{id: child, directory: "/repo", parentID: &parent},
		{id: "grandchild", directory: "/repo", parentID: &child},
	})
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A pre-existing request must retain its original time when replayed.
	if _, err := store.RecordSessionHalt(t.Context(), "grandchild", "question", "q", 120002); err != nil {
		t.Fatal(err)
	}
	a := New(database, store)
	prompt := platforms.LivePrompt{"id": "q", "sessionID": "grandchild"}
	a.ObservePromptAsked("", "/repo", "question", prompt)
	a.ObservePromptResolved("/repo", "question", "grandchild", "q")
	a.ObservePromptAsked("", "/repo", "question", prompt)
	for _, adapter := range []*Adapter{a, New(database, store)} {
		list, err := adapter.Sessions(t.Context(), "/repo", 0)
		if err != nil {
			t.Fatal(err)
		}
		summary, err := adapter.SessionSummary(t.Context(), parent)
		if err != nil {
			t.Fatal(err)
		}
		detail := &platforms.SessionDetail{Session: &db.Session{ID: parent, Directory: "/repo"}}
		if err := adapter.attachSessionTree(t.Context(), parent, detail); err != nil {
			t.Fatal(err)
		}
		for _, row := range []db.Session{list[0], *summary, *detail.Session} {
			if row.LastHaltAt != 120002 {
				t.Fatalf("parent halt = %d, want 120002", row.LastHaltAt)
			}
		}
	}
}

func TestSessionHaltSnapshotDeduplicatesInMemory(t *testing.T) {
	a := New(newTestDBWithSession(t, "s", "/repo"), nil)
	prompt := platforms.LivePrompt{"id": "p", "sessionID": "s"}
	a.prompts.applySnapshot("/repo", "permission", 1, []platforms.LivePrompt{prompt}, nil)
	first := a.prompts.lastHalts["s"]
	if first <= 0 {
		t.Fatal("missing snapshot halt time")
	}
	a.ObservePromptResolved("/repo", "permission", "s", "p")
	a.ObservePromptAsked("", "/repo", "permission", prompt)
	if a.prompts.lastHalts["s"] != first {
		t.Fatal("replay promoted old permission")
	}
}
