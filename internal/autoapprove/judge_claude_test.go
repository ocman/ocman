package autoapprove

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// fakeClaude writes a stand-in claude that answers every -p call with reply
// and records its argv and prompt. The real CLI never runs.
func fakeClaude(t *testing.T, reply string) (cli, record string) {
	t.Helper()
	dir := t.TempDir()
	cli, record = filepath.Join(dir, "claude"), filepath.Join(dir, "call")
	script := "#!/bin/sh\necho \"$@\" > " + record + "\ncat >> " + record + "\nprintf '%s' '" + reply + "'\n"
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return cli, record
}

func TestClaudeJudge(t *testing.T) {
	cli, record := fakeClaude(t, `{"verdict":"safe","reasoning":"Lists files."}`)
	result := ClaudeJudge{CLI: cli}.JudgeWithCallback(t.Context(), t.TempDir(), "bash", []string{"ls"}, map[string]any{"command": "ls"}, nil, nil)
	if result.Verdict != verdictSafe || result.EvaluationFailed || result.Reasoning != "Lists files." {
		t.Fatalf("result = %+v", result)
	}
	call, _ := os.ReadFile(record)
	if !strings.HasPrefix(string(call), "-p --model haiku --no-session-persistence --strict-mcp-config --tools  --output-format text") ||
		!strings.Contains(string(call), "ls") {
		t.Fatalf("call = %s", call)
	}
	failed := ClaudeJudge{CLI: filepath.Join(t.TempDir(), "missing")}.JudgeWithCallback(t.Context(), "/nowhere", "bash", nil, nil, nil, nil)
	if failed.Verdict != verdictUnsafe || !failed.EvaluationFailed {
		t.Fatalf("missing CLI = %+v", failed)
	}
	// The judge runs inside a helper slot, released afterwards; without
	// one it fails (needs the user) and never runs the CLI.
	_ = os.Remove(record)
	var taken, released int
	slot := func(context.Context) (func(), error) { taken++; return func() { released++ }, nil }
	if r := (ClaudeJudge{CLI: cli, Slot: slot}).JudgeWithCallback(t.Context(), t.TempDir(), "bash", []string{"ls"}, nil, nil, nil); r.Verdict != verdictSafe || taken != 1 || released != 1 {
		t.Fatalf("slotted judge = %+v taken=%d released=%d", r, taken, released)
	}
	_ = os.Remove(record)
	none := func(context.Context) (func(), error) { return nil, context.DeadlineExceeded }
	if r := (ClaudeJudge{CLI: cli, Slot: none}).JudgeWithCallback(t.Context(), t.TempDir(), "bash", []string{"ls"}, nil, nil, nil); !r.EvaluationFailed {
		t.Fatalf("judge without a slot = %+v", r)
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("the CLI ran without a helper slot")
	}
}

func TestFallbackJudgeWithoutOpenCode(t *testing.T) {
	cli, _ := fakeClaude(t, `{"verdict":"unsafe","reasoning":"Deletes data."}`)
	s := &Service{deps: Deps{FallbackJudge: ClaudeJudge{CLI: cli}}, judge: &PermissionJudge{openCodePort: func(string) string { return "" }}}
	if got := s.judgePermission(t.Context(), "claudecode", t.TempDir(), "bash", []string{"rm -rf /"}, nil, nil, nil); got.Verdict != verdictUnsafe || got.Reasoning != "Deletes data." {
		t.Fatalf("result = %+v", got)
	}
}

func TestHandleProviderEvent(t *testing.T) {
	var mu sync.Mutex
	var needs []string
	s := NewService(Deps{PromptNeedsUser: func(_, sessionID, kind, requestID string) {
		mu.Lock()
		needs = append(needs, kind+":"+requestID)
		mu.Unlock()
	}})
	adapter := &fakePlatform{id: "claudecode"}
	send := func(typ string, props any) {
		data, _ := json.Marshal(props)
		s.HandleProviderEvent(context.Background(), adapter, providerprotocol.Event{Type: typ, Properties: data})
	}
	send("permission.asked", map[string]any{"id": "per_1", "sessionID": "s", "permission": "bash", "patterns": []string{"ls"}, "metadata": map[string]any{"command": "ls"}})
	send("question.asked", map[string]any{"id": "que_1", "sessionID": "s"})
	send("permission.replied", map[string]any{"sessionID": "s", "requestID": "per_1", "reply": "reject", "reason": "cancelled"})
	send("permission.replied", map[string]any{"sessionID": "s", "requestID": "per_2", "reply": "once"})
	s.HandleProviderEvent(context.Background(), adapter, providerprotocol.Event{Type: "permission.asked", Properties: []byte("{")})
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(needs)
		mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("needs user = %v", needs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := s.getAsked("s", "per_1"); ok {
		t.Fatal("a cancelled prompt must be forgotten, not recorded as a user reply")
	}
}

// Provider replies resolve the prompt under the adapter's own platform.
func TestHandleProviderEventBroadcastsResolutionsWithPlatform(t *testing.T) {
	var got []string
	record := func(kind string) func(platformID, sessionID, requestID, reason string) {
		return func(platformID, sessionID, requestID, reason string) {
			got = append(got, strings.Join([]string{kind, platformID, sessionID, requestID, reason}, " "))
		}
	}
	s := NewService(Deps{BroadcastPermissionResolved: record("permission"), BroadcastQuestionResolved: record("question")})
	adapter := &fakePlatform{id: "claudecode"}
	send := func(typ string, props any) {
		data, _ := json.Marshal(props)
		s.HandleProviderEvent(context.Background(), adapter, providerprotocol.Event{Type: typ, Properties: data})
	}
	send("permission.replied", map[string]any{"sessionID": "s", "requestID": "p1", "reply": "reject", "reason": "cancelled"})
	send("permission.replied", map[string]any{"sessionID": "s", "requestID": "p2", "reply": "always"})
	send("permission.replied", map[string]any{"sessionID": "s", "requestID": "p3", "reply": "reject"})
	send("question.replied", map[string]any{"sessionID": "s", "requestID": "q1"})
	send("question.rejected", map[string]any{"sessionID": "s", "requestID": "q2"})
	want := []string{
		"permission claudecode s p2 replied",
		"permission claudecode s p3 rejected",
		"question claudecode s q1 replied",
		"question claudecode s q2 rejected",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("resolutions = %q, want %q", got, want)
	}
}

// A Claude-only ocman never discovers OpenCode to judge (FR-1/AD-1): the
// OpenCode judge is off and the fallback decides.
func TestOpenCodeDisabledSkipsOpenCodeJudge(t *testing.T) {
	cli, _ := fakeClaude(t, `{"verdict":"safe","reasoning":"Read-only."}`)
	s := NewService(Deps{OpenCodeDisabled: true, FallbackJudge: ClaudeJudge{CLI: cli}})
	if s.judge.openCodePort != nil {
		t.Fatal("OpenCode port discovery wired while OpenCode is disabled")
	}
	if got := s.judgePermission(t.Context(), "claudecode", t.TempDir(), "bash", []string{"ls"}, nil, nil, nil); got.Verdict != verdictSafe {
		t.Fatalf("result = %+v", got)
	}
}
