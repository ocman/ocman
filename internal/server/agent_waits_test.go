package server

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestAgentWaitsObserveQuestionsAndManualPermissions(t *testing.T) {
	s := testServer(t)
	before := time.Now().UnixMilli() - 1
	// Exercise the actual pipeline hook. Questions always need the user;
	// permissions also do when the autoapproval setting is off.
	s.aaSvc().ObserveQuestionPrompt(platforms.ID("opencode"), platforms.LivePrompt{"sessionID": "s", "id": "q"})
	s.onPromptNeedsUser("opencode", "s", "permission", "p")
	waits, err := s.stateDB.AgentUserWaits(t.Context(), "opencode", before, time.Now().UnixMilli()+1000)
	if err != nil || len(waits) != 2 {
		t.Fatalf("waits %v, %v", waits, err)
	}
	s.onAgentPromptResolved("s", "question", "q")
	s.onAgentWaitIdle("opencode", "s")
	s.onPromptNeedsUser("opencode", "s", "question", "q")
	if _, err := s.stateDB.AgentUserWaits(t.Context(), "opencode", before, time.Now().UnixMilli()+1000); err != nil {
		t.Fatal(err)
	}
	// Safe autoapproval never calls onPromptNeedsUser, so a resolution alone
	// creates no measurable human wait.
	s.onAgentPromptResolved("safe", "permission", "p")
	s.stateDB = nil
	s.onAgentPromptResolved("missing", "permission", "p")
	s.onPromptNeedsUser("opencode", "missing", "question", "q")
}
