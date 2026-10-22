package autoapprove

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestOpenCodeJudgeThroughInterface(t *testing.T) {
	server := fakeLifecycleJudgeServer(t, `{"verdict":"safe","reasoning":"Read only."}`, false)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var backend Judge = &PermissionJudge{
		openCodePort: func(string) string { return port },
		httpClient:   server.Client(),
	}
	created := false
	result := backend.JudgeWithCallback(t.Context(), "/project", "read", nil, nil, nil, func(string) { created = true })
	if !created || result.Verdict != verdictSafe || result.EvaluationFailed || result.SessionID != "" || result.Reasoning != "Read only." {
		t.Fatalf("created=%v, result=%+v", created, result)
	}
}

func TestJudgeWithoutBackendNeedsUser(t *testing.T) {
	for _, backend := range []*PermissionJudge{
		nil,
		{},
		{openCodePort: func(string) string { return "" }},
	} {
		var judge Judge = backend
		result := judge.JudgeWithCallback(t.Context(), "/project", "read", nil, nil, nil, nil)
		if result.Verdict != verdictNeedsUser || result.EvaluationFailed {
			t.Fatalf("result=%+v", result)
		}
	}
	for _, selection := range []string{"nil judge", "no port discovery", "no running instance", "selector unavailable"} {
		t.Run(selection, func(t *testing.T) {
			recorder := &needsUserRecorder{}
			store := &lifecycleStore{}
			s := &Service{deps: Deps{
				Store:           store,
				DefaultEnabled:  true,
				SessionDir:      func(string) (string, error) { return "/project", nil },
				PromptNeedsUser: recorder.deps(),
			}}
			switch selection {
			case "no port discovery":
				s.judge = &PermissionJudge{}
			case "no running instance":
				s.judge = &PermissionJudge{openCodePort: func(string) string { return "" }}
			case "selector unavailable":
				s.deps.SelectJudge = func(id platforms.ID, directory string) Judge {
					if id != "other" || directory != "/project" {
						t.Fatalf("selection target=%s %s", id, directory)
					}
					return nil
				}
			}
			adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
				t.Fatal("unavailable judge must leave the permission unanswered")
				return nil
			}}
			s.backgroundAutoApprove(t.Context(), "other", adapter, "session", "permission", askedPermission{platformID: "other", permission: "read"})
			verdict, ok := lookupJudged(s, "session", "permission")
			if !ok || verdict != verdictNeedsUser || len(recorder.calls) != 1 {
				t.Fatalf("verdict=%s, ok=%v, needs-user=%v", verdict, ok, recorder.calls)
			}
			row := store.waitFor(t, "permission", func(row state.PermissionLifecycle) bool { return row.JudgeCompletedAt > 0 })
			if row.EvaluationResult != state.PermissionEvaluationError || row.Resolution != "" {
				t.Fatalf("unavailable backend must not count as an unsafe verdict or resolve the prompt: %+v", row)
			}
			var events []string
			s.deps.BroadcastGlobalEvent = func(event string, _ []byte) { events = append(events, event) }
			s.SurfacePermissionNotification("session", "permission")
			if len(events) != 1 || events[0] != "ocman.permission.flagged" {
				t.Fatalf("events=%v", events)
			}
			var replay strings.Builder
			s.RegisterSink("session", &replay, nil)
			s.replayAutoApproveState("session", "permission", "read", nil)
			if !strings.Contains(replay.String(), "ocman.permission.flagged") {
				t.Fatalf("needs-user verdict was not replayed: %s", replay.String())
			}
		})
	}
}

type recordingJudge struct{ calls int }

func (j *recordingJudge) JudgeWithCallback(context.Context, string, string, []string, map[string]any, []PromptSection, func(string)) JudgeResult {
	j.calls++
	return JudgeResult{Verdict: verdictSafe, Reasoning: "Read only."}
}

func TestSafeCommandCachePrecedesBackendSelection(t *testing.T) {
	backend := &recordingJudge{}
	selections, replies := 0, 0
	s := &Service{deps: Deps{
		DefaultEnabled: true,
		SessionDir:     func(string) (string, error) { return "/project", nil },
		SelectJudge: func(platforms.ID, string) Judge {
			selections++
			return backend
		},
	}}
	adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error { replies++; return nil }}
	asked := askedPermission{platformID: "other", permission: "bash", metadata: map[string]any{"command": "ls"}}
	s.backgroundAutoApprove(t.Context(), "other", adapter, "session", "first", asked)
	s.backgroundAutoApprove(t.Context(), "other", adapter, "session", "cached", asked)
	if selections != 1 || backend.calls != 1 || replies != 2 {
		t.Fatalf("selections=%d, backend calls=%d, replies=%d", selections, backend.calls, replies)
	}
	// A different request selects a backend again, rather than reusing a
	// process-wide choice made for the first permission.
	asked.metadata = map[string]any{"command": "pwd"}
	s.backgroundAutoApprove(t.Context(), "other", adapter, "session", "different", asked)
	if selections != 2 || backend.calls != 2 || replies != 3 {
		t.Fatalf("selections=%d, backend calls=%d, replies=%d", selections, backend.calls, replies)
	}
}

type panickingJudge struct{}

func (panickingJudge) JudgeWithCallback(context.Context, string, string, []string, map[string]any, []PromptSection, func(string)) JudgeResult {
	panic("malformed judge reply")
}

// A panicking judge made no safety assessment, so it is a failed evaluation
// like an unreachable backend. Recovering only around the whole goroutine
// skipped the lifecycle completion and the unsafe-verdict path: the status
// was dropped, a connected browser stayed "checking", and the prompt was
// never reported as needing the user.
func TestPanickingJudgeLeavesThePromptForTheUser(t *testing.T) {
	var mu sync.Mutex
	var needs []string
	store := &lifecycleStore{}
	s := NewService(Deps{
		Store:          store,
		DefaultEnabled: true,
		SessionDir:     func(string) (string, error) { return "/project", nil },
		SelectJudge:    func(platforms.ID, string) Judge { return panickingJudge{} },
		PromptNeedsUser: func(platformID, sessionID, kind, requestID string) {
			mu.Lock()
			needs = append(needs, platformID+"|"+sessionID+"|"+kind+"|"+requestID)
			mu.Unlock()
		},
	})
	var sink strings.Builder
	s.RegisterSink("session", &sink, nil)
	s.Ensure("other", &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		t.Error("a panicking judge must leave the permission unanswered")
		return nil
	}}, "session", "permission", "bash", []string{"ls"}, map[string]any{"command": "ls"})
	s.jobs.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(needs) != 1 || needs[0] != "other|session|permission|permission" {
		t.Fatalf("needs-user = %v", needs)
	}
	if verdict, ok := lookupJudged(s, "session", "permission"); !ok || verdict != verdictUnsafe {
		t.Fatalf("verdict = %q, %v; want the failure recorded", verdict, ok)
	}
	if !strings.Contains(sink.String(), "event: ocman.permission.flagged") {
		t.Fatalf("sink never left the checking state: %s", sink.String())
	}
	row := store.waitFor(t, "permission", func(row state.PermissionLifecycle) bool { return row.JudgeCompletedAt > 0 })
	if row.EvaluationResult != state.PermissionEvaluationError || row.Resolution != "" {
		t.Fatalf("lifecycle = %+v; want an evaluation error, unresolved", row)
	}
}
