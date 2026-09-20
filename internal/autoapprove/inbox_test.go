package autoapprove

import (
	"github.com/NoUseFreak/ocman/internal/platforms"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestObservedPromptNotifiesInboxWithJudgeDisabled(t *testing.T) {
	var got state.InboxPermission
	svc := NewService(Deps{PermissionAsked: func(permission state.InboxPermission) { got = permission }})
	svc.ObservePermissionPrompt("opencode", "parent-session", platforms.LivePrompt{"sessionID": "child-session", "id": "permission-1", "permission": "bash", "patterns": []string{"git status"}, "always": []string{"git *"}, "metadata": map[string]any{"command": "git status"}})
	if got.Platform != "opencode" || got.SessionID != "child-session" || got.PermissionID != "permission-1" || got.Permission != "bash" || got.Metadata["command"] != "git status" || len(got.Always) != 1 || got.Always[0] != "git *" {
		t.Fatalf("permission notification: %+v", got)
	}
}
