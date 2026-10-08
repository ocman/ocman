package opencode

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestNotificationPrompts(t *testing.T) {
	a := New(newTestDBWithSession(t, "child", "/repo"), nil)
	permissions, questions, err := a.NotificationPrompts(t.Context(), "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(permissions)+len(questions) != 0 {
		t.Fatal("empty observed prompt cache returned prompts")
	}
	a.ObservePromptAsked("", "/repo", "permission", platforms.LivePrompt{"id": "p1", "sessionID": "child"})
	a.ObservePromptAsked("", "/repo", "question", platforms.LivePrompt{"id": "q1", "sessionID": "child"})
	permissions, questions, err = a.NotificationPrompts(t.Context(), "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(permissions) != 1 || permissions[0]["id"] != "p1" || len(questions) != 1 || questions[0]["id"] != "q1" {
		t.Fatalf("permissions=%+v questions=%+v", permissions, questions)
	}
}

func TestNotificationPromptsPreservesChildLookupFailure(t *testing.T) {
	parent := "parent"
	a := New(newTestDBWithSessions(t, []testSession{{id: "child", directory: "/repo", parentID: &parent}}), nil)
	a.ObservePromptAsked("", "/repo", "permission", platforms.LivePrompt{"id": "p1", "sessionID": "child"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := a.NotificationPrompts(ctx, parent); err == nil {
		t.Fatal("failed ancestor lookup was reported as an empty successful snapshot")
	}
	a.ObservePromptAsked("", "/repo", "question", platforms.LivePrompt{"id": "q1", "sessionID": "child"})
	a.ObservePromptResolved("/repo", "permission", "child", "p1")
	if _, _, err := a.NotificationPrompts(ctx, parent); err == nil {
		t.Fatal("failed question ancestor lookup was reported as an empty successful snapshot")
	}
}
