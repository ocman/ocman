package opencode

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSidebarSnapshotUsesOnlySeededCaches(t *testing.T) {
	a := New(nil, nil)
	a.ObserveSessionStatus("7777", 0, "root", "busy")
	if got := a.CachedSessionObservations(); len(got) != 0 {
		t.Fatal("unseeded source supplied live status")
	}
	a.turns.seeded["7777"] = 1
	a.ObservePromptAsked("7777", "/repo", "permission", platforms.LivePrompt{"sessionID": "child", "id": "p"})
	a.ObservePromptAsked("7777", "/repo", "question", platforms.LivePrompt{"sessionID": "root", "id": "q"})
	got := a.CachedSessionObservations()
	if got["root"].Status != "busy" || !got["root"].PendingQuestion || !got["child"].PendingPermission {
		t.Fatalf("cached observations=%+v", got)
	}
	a.ObserveSessionStatus("7777", 0, "root", "idle")
	if live := a.CachedSessionObservations()["root"]; live.Status != "idle" {
		t.Fatalf("settled cache invented a completed outcome: %+v", live)
	}
	a.ClearSessionStatusForPort("7777")
	a.ClearPromptsForPort("7777")
	if got := a.CachedSessionObservations(); len(got) != 0 {
		t.Fatalf("disconnected observations remained: %+v", got)
	}
}
