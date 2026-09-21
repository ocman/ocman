package autoapprove

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// needsUserRecorder collects every prompt the pipeline hands to the user.
type needsUserRecorder struct {
	calls []string
}

func (r *needsUserRecorder) deps() func(string, string, string, string) {
	return func(platformID, sessionID, kind, requestID string) {
		r.calls = append(r.calls, platformID+"|"+sessionID+"|"+kind+"|"+requestID)
	}
}

// TestPromptNeedsUserFollowsTheApprovalDecision pins where the hook fires. It
// is the guard on the one way an attention notice could undermine
// auto-approval: reporting on the raw permission.asked edge would page a user
// for every command the judge is about to approve by itself, which trains the
// reader to ignore the notices that actually matter.
func TestPromptNeedsUserFollowsTheApprovalDecision(t *testing.T) {
	const sessionID, permissionID = "ses-1", "perm-1"
	safeCommand := map[string]any{"command": "pnpm test"}

	for _, tc := range []struct {
		name     string
		enabled  bool
		metadata map[string]any
		seedSafe bool
		want     bool
	}{
		{
			name:     "disabled hands every prompt straight to the user",
			enabled:  false,
			metadata: safeCommand,
			want:     true,
		},
		{
			name:     "an approved permission is never the user's problem",
			enabled:  true,
			metadata: safeCommand,
			seedSafe: true,
			want:     false,
		},
		{
			name:     "a denied permission is the user's to answer",
			enabled:  true,
			metadata: map[string]any{"command": "rm -rf /"},
			want:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &needsUserRecorder{}
			s := &Service{
				sseSessions:      make(map[string]map[*Sink]struct{}),
				autoApprove:      make(map[string]*autoApproveStatus),
				safeCommandCache: make(map[string]map[string]string),
				askedCache:       make(map[string]askedPermission),
				deps: Deps{
					DefaultEnabled:  tc.enabled,
					PromptNeedsUser: recorder.deps(),
				},
				// judge=nil: reaching the LLM panics, so every case here has
				// to be settled by the enablement, cache or denylist gates.
			}
			if tc.seedSafe {
				s.recordSafeCommandVerdict(sessionID, commandHash(tc.metadata), "Read-only test runner.")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s.backgroundAutoApprove(ctx, platforms.ID("opencode"), &fakePlatform{id: "opencode"},
				sessionID, permissionID,
				askedPermission{platformID: "opencode", permission: "Bash command", metadata: tc.metadata})

			if got := len(recorder.calls) > 0; got != tc.want {
				t.Fatalf("reported needs-user = %v, want %v (calls: %v)", got, tc.want, recorder.calls)
			}
			if tc.want && recorder.calls[0] != "opencode|"+sessionID+"|permission|"+permissionID {
				t.Fatalf("reported %q", recorder.calls[0])
			}
		})
	}
}

// TestObserveQuestionPromptNeedsUser covers the other half of the lifecycle: a
// question has no approval pipeline at all, so asking it is already the
// decision that only the user can answer it.
func TestObserveQuestionPromptNeedsUser(t *testing.T) {
	recorder := &needsUserRecorder{}
	s := &Service{deps: Deps{PromptNeedsUser: recorder.deps()}}

	s.ObserveQuestionPrompt("opencode", platforms.LivePrompt{"sessionID": "ses-1", "id": "q-1"})
	// A prompt missing either half of its identity has no stable key, so it is
	// dropped rather than reported under one that would collide.
	s.ObserveQuestionPrompt("opencode", platforms.LivePrompt{"id": "q-2"})
	s.ObserveQuestionPrompt("opencode", platforms.LivePrompt{"sessionID": "ses-1"})
	s.ObserveQuestionPrompt("opencode", platforms.LivePrompt{})

	if len(recorder.calls) != 1 || recorder.calls[0] != "opencode|ses-1|question|q-1" {
		t.Fatalf("reported %v", recorder.calls)
	}

	// A nil hook and a nil service must both stay silent rather than panic:
	// every Deps func field is optional.
	(&Service{}).ObserveQuestionPrompt("opencode", platforms.LivePrompt{"sessionID": "ses-1", "id": "q-1"})
	var nilService *Service
	nilService.ObserveQuestionPrompt("opencode", platforms.LivePrompt{"sessionID": "ses-1", "id": "q-1"})
}
