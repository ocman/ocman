package server

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
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
	s.onAgentPromptResolved("opencode", "s", "question", "q")
	s.onAgentWaitIdle("opencode", "s")
	s.onPromptNeedsUser("opencode", "s", "question", "q")
	if _, err := s.stateDB.AgentUserWaits(t.Context(), "opencode", before, time.Now().UnixMilli()+1000); err != nil {
		t.Fatal(err)
	}
	// Safe autoapproval never calls onPromptNeedsUser, so a resolution alone
	// creates no measurable human wait.
	s.onAgentPromptResolved("opencode", "safe", "permission", "p")
	s.stateDB = nil
	s.onAgentPromptResolved("opencode", "missing", "permission", "p")
	s.onPromptNeedsUser("opencode", "missing", "question", "q")
}

func TestProviderIdleResolvesOwnerWaits(t *testing.T) {
	s := testServer(t)
	start := time.Now().UnixMilli() - 1000
	for _, platform := range []string{"opencode", "claudecode"} {
		if err := s.stateDB.StartAgentUserWait(t.Context(), platform, "s", "question", "q", start); err != nil {
			t.Fatal(err)
		}
	}
	s.sessionEventSink().OnSessionIdle("claudecode", "s")
	until := time.Now().UnixMilli() + 10000
	waits, err := s.stateDB.AgentUserWaits(t.Context(), "claudecode", start-1, until)
	if err != nil || len(waits) == 0 {
		t.Fatalf("waits=%v, %v", waits, err)
	}
	resolved := false
	for _, wait := range waits {
		if wait.End < until {
			resolved = true
		}
	}
	if !resolved {
		t.Fatal("provider wait remained open")
	}
	other, err := s.stateDB.AgentUserWaits(t.Context(), "opencode", start-1, until)
	if err != nil || len(other) != 1 || other[0].End != until {
		t.Fatalf("other owner's wait=%v, %v", other, err)
	}
}
