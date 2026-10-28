package opencode

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/state"
)

func TestSessionHaltsSurviveResolutionReplayAndAdapterRestart(t *testing.T) {
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
	a := New(database, store, nil, ocapi.Auth{})
	prompt := platforms.LivePrompt{"id": "q", "sessionID": "grandchild"}
	a.ObservePromptAsked("", "/repo", "question", prompt)
	a.ObservePromptResolved("/repo", "question", "grandchild", "q")
	a.ObservePromptAsked("", "/repo", "question", prompt)
	parents := map[string]string{child: parent, "grandchild": child}
	for _, adapter := range []*Adapter{a, New(database, store, nil, ocapi.Auth{})} {
		rows := []db.Session{{ID: parent, Directory: "/repo"}}
		if err := adapter.enrichStoredHalts(t.Context(), rows, parents); err != nil {
			t.Fatal(err)
		}
		if rows[0].LastHaltAt != 120002 {
			t.Fatalf("parent halt = %d, want 120002", rows[0].LastHaltAt)
		}
	}
}

func TestSessionHaltSnapshotDeduplicatesInMemory(t *testing.T) {
	a := New(newTestDBWithSession(t, "s", "/repo"), nil, nil, ocapi.Auth{})
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
