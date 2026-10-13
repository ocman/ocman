package claudeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
)

const markerCommand = `python3 -c "from pathlib import Path; Path('permission-marker.txt').write_text('SPIKE_PERMISSION')"`

// seen records the prompt events the headless pipeline received.
type seen struct {
	mu     sync.Mutex
	events []providerprotocol.Event
}

func (s *seen) add(e providerprotocol.Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *seen) props(typ string) []platforms.LivePrompt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []platforms.LivePrompt
	for _, e := range s.events {
		if e.Type == typ {
			var p platforms.LivePrompt
			_ = json.Unmarshal(e.Properties, &p)
			out = append(out, p)
		}
	}
	return out
}

// promptHarness serves fixture with ocman-owned rules and a headless handler.
func promptHarness(t *testing.T, fixturePath string, handler func(*liveHarness, providerprotocol.Event), watch ...bool) (*liveHarness, *seen) {
	t.Helper()
	t.Setenv("FAKE_CLAUDE_STDIN", filepath.Join(t.TempDir(), "stdin.jsonl"))
	// A short idle timeout lets the next turn resume a fresh replay.
	h := serveLive(t, fixturePath, func(o *Options) { o.IdleTimeout = 50 * time.Millisecond })
	rules, err := provider.OpenRules(filepath.Join(h.dir, "rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.adapter.SetRules(rules)
	got := &seen{}
	h.adapter.SetEventHandler(func(e providerprotocol.Event) {
		got.add(e)
		if handler != nil {
			handler(h, e)
		}
	})
	if len(watch) == 0 || watch[0] {
		go func() { _ = h.adapter.WatchEvents(t.Context()) }()
	}
	return h, got
}

func stdinLog(t *testing.T) string {
	t.Helper()
	data, _ := os.ReadFile(os.Getenv("FAKE_CLAUDE_STDIN"))
	return string(data)
}

func (h *liveHarness) turn(t *testing.T, id string, idles int) {
	t.Helper()
	waitFor(t, "previous child stopped", func() bool { h.live.mu.Lock(); defer h.live.mu.Unlock(); return h.live.procs[id] == nil })
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == idles })
}

// A rule-allowed prompt is answered by ocman's rules: it never reaches the
// browser, the pending list or the judge, and nothing polls for it.
func TestRuleAllowedPromptNeverReachesBrowser(t *testing.T) {
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), nil)
	id := h.create(t)
	if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: id,
		Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "python3 *", Action: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = h.adapter.ProxyEvents(ctx, id, lockedWriter{&mu, &buf}, func() {}) }()
	h.turn(t, id, 1)
	waitFor(t, "browser saw the reply", func() bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(buf.String(), "DONE") })

	mu.Lock()
	browser := buf.String()
	mu.Unlock()
	if strings.Contains(browser, "permission.asked") {
		t.Fatalf("rule-allowed prompt reached the browser:\n%s", browser)
	}
	if n := len(got.props("permission.asked")) + len(got.props("permission.replied")); n != 0 {
		t.Fatalf("rule-allowed prompt reached the pipeline: %+v", got.events)
	}
	if pending, _ := h.adapter.ListPermissions(t.Context(), id); len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
	if log := stdinLog(t); !strings.Contains(log, `"behavior":"allow"`) || !strings.Contains(log, "permission-marker.txt") {
		t.Fatalf("claude was not answered with the original input: %s", log)
	}
	args := h.launchArgs(t)
	if args[slices.Index(args, "--permission-prompt-tool")+1] != "stdio" || args[slices.Index(args, "--permission-mode")+1] != "default" {
		t.Fatalf("launch args = %q", args)
	}
	if rules, err := h.adapter.PermissionRules(t.Context(), id); err != nil || len(rules) != 1 {
		t.Fatalf("rules = %+v %v", rules, err)
	}
	if data, err := os.ReadFile(filepath.Join(h.dir, "rules.json")); err != nil || !strings.Contains(string(data), "python3 *") {
		t.Fatalf("rules are not persisted: %s %v", data, err)
	}
}

// liveReader reads a parent's live ruleset, as the worktree launch does.
type liveReader struct {
	ctx     context.Context
	adapter *provider.Adapter
}

func (r liveReader) PermissionRules(_, id string) ([]platforms.PermissionRule, error) {
	return r.adapter.PermissionRules(r.ctx, id)
}

// A YOLO parent's worktree child inherits YOLO and runs unprompted.
func TestYOLOParentChildIsNotPrompted(t *testing.T) {
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), nil)
	parent, child := h.create(t), h.create(t)
	yolo := []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}
	if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: parent, Rules: yolo}); err != nil {
		t.Fatal(err)
	}
	rules, _, err := permissions.BuildInheritedRulesWithLive(t.Context(), nil, liveReader{t.Context(), h.adapter}, PlatformID, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: child, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	h.turn(t, child, 1)
	if asked := got.props("permission.asked"); len(asked) != 0 {
		t.Fatalf("YOLO child was prompted: %+v", asked)
	}
}

// A user's Allow-always reply is recorded with user provenance, allows the
// same call in that session, and a worktree child inherits it.
func TestAllowAlwaysIsRecordedAndInherited(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := autoapprove.NewService(autoapprove.Deps{Store: st})
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), func(h *liveHarness, e providerprotocol.Event) {
		svc.HandleProviderEvent(t.Context(), h.adapter, e)
	})
	parent := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: parent, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	var pending []platforms.LivePrompt
	waitFor(t, "pending prompt", func() bool { pending, _ = h.adapter.ListPermissions(t.Context(), parent); return len(pending) == 1 })
	if pending[0]["permission"] != "bash" || pending[0]["patterns"].([]any)[0] != markerCommand {
		t.Fatalf("prompt = %+v", pending[0])
	}
	if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: parent,
		PermissionID: pending[0]["id"].(string), Reply: "always"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "parent turn idle", func() bool { _, _, n := h.events.get(parent); return n == 1 })
	var approved []state.ApprovedPermission
	waitFor(t, "recorded approval", func() bool {
		approved, _ = st.ListApprovedPermissions(t.Context(), PlatformID, parent)
		return len(approved) == 1
	})
	if a := approved[0]; a.ApprovedBy != state.ApprovalActorUser || a.Reply != state.ApprovalReplyAlways || a.PermissionText != "bash" {
		t.Fatalf("approval = %+v", a)
	}
	// The same call in the same session is now allowed without asking.
	h.turn(t, parent, 2)
	if n := len(got.props("permission.asked")); n != 1 {
		t.Fatalf("parent asked %d times", n)
	}

	child := h.create(t)
	rules, _, err := permissions.BuildInheritedRules(t.Context(), st, PlatformID, parent)
	if err != nil || len(rules) != 1 || rules[0].Pattern != markerCommand {
		t.Fatalf("inherited = %+v %v", rules, err)
	}
	if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: child, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	h.turn(t, child, 1)
	if n := len(got.props("permission.asked")); n != 1 {
		t.Fatalf("child was prompted despite the inherited approval")
	}
}

// With no OpenCode instance, the Claude CLI judge decides the permission.
func TestClaudeJudgeDecidesWithoutOpenCode(t *testing.T) {
	judgeLog := filepath.Join(t.TempDir(), "judge.json")
	t.Setenv("FAKE_CLAUDE_PRINT", `{"verdict":"safe","reasoning":"writes a marker in the project"}`)
	t.Setenv("FAKE_CLAUDE_PRINT_LOG", judgeLog)
	var svc *autoapprove.Service
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), func(h *liveHarness, e providerprotocol.Event) {
		svc.HandleProviderEvent(t.Context(), h.adapter, e)
	})
	svc = autoapprove.NewService(autoapprove.Deps{DefaultEnabled: true,
		SessionDir:    func(string) (string, error) { return h.dir, nil },
		FallbackJudge: autoapprove.ClaudeJudge{CLI: fakeBinary.path}})
	id := h.create(t)
	// Claude records the user's message before asking; the fake does not
	// write transcripts, so place the line it would have written.
	transcript := filepath.Join(h.live.opts.ConfigDir, "projects", "-intent", id+".jsonl")
	line := `{"type":"user","cwd":"` + h.dir + `","sessionId":"` + id + `","uuid":"u-1","timestamp":"2026-10-08T09:00:00.000Z","message":{"role":"user","content":"go"}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.live.idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.turn(t, id, 1)
	if len(got.props("permission.asked")) != 1 || len(got.props("permission.replied")) != 1 || got.props("permission.replied")[0]["reply"] != "once" {
		t.Fatalf("events = %+v", got.events)
	}
	var call struct {
		Args   []string `json:"args"`
		Prompt string   `json:"prompt"`
	}
	if err := json.Unmarshal(must(os.ReadFile(judgeLog)), &call); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(call.Args, autoapprove.ClaudeJudgeArgs) || !strings.Contains(call.Prompt, "permission-marker.txt") {
		t.Fatalf("judge call = %+v", call)
	}
	// The judge sees the Claude session's recent user intent (FR-5 / AD-8).
	if !strings.Contains(call.Prompt, "Recent user intent") || !strings.Contains(call.Prompt, "  - go") {
		t.Fatalf("judge prompt lacks the user's intent:\n%s", call.Prompt)
	}
}

// AskUserQuestion is answered from ocman and the turn continues.
func TestAskUserQuestionAnsweredFromOcman(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "question.jsonl")
	rows := `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"question-1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","display_name":"AskUserQuestion","input":{"questions":[{"question":"Which marker?","header":"Marker","options":[{"label":"Alpha","description":"first marker"},{"label":"Beta","description":"second marker"}],"multiSelect":false}]},"tool_use_id":"toolu_question1","requires_user_interaction":true}}}
{"direction":"stdin","message":{"type":"control_response"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_q","model":"haiku","content":[{"type":"text","text":"Alpha it is"}]}}}
{"direction":"stdout","message":{"type":"result","is_error":false}}
`
	if err := os.WriteFile(fixturePath, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	h, got := promptHarness(t, fixturePath, nil)
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "ask"}); err != nil {
		t.Fatal(err)
	}
	var questions []platforms.LivePrompt
	waitFor(t, "question", func() bool { questions, _ = h.adapter.ListQuestions(t.Context(), id); return len(questions) == 1 })
	q := questions[0]["questions"].([]any)[0].(map[string]any)
	if q["question"] != "Which marker?" || len(q["options"].([]any)) != 2 || len(got.props("question.asked")) != 1 {
		t.Fatalf("question = %+v", questions[0])
	}
	if err := h.adapter.RespondQuestion(t.Context(), platforms.RespondQuestionRequest{SessionID: id,
		RequestID: questions[0]["id"].(string), Answers: [][]string{{"Alpha"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if log := stdinLog(t); !strings.Contains(log, `"answers":{"Which marker?":"Alpha"}`) || !strings.Contains(log, `"header":"Marker"`) {
		t.Fatalf("answer = %s", log)
	}
	if err := h.adapter.RejectQuestion(t.Context(), platforms.RejectQuestionRequest{SessionID: id, RequestID: "que_gone"}); err == nil {
		t.Fatal("rejecting an answered question must fail")
	}
}

// A prompt asked while ocman is disconnected is re-sent from the spool on
// reconnect, and its answer completes the turn.
func TestPendingPromptAnsweredAfterReconnect(t *testing.T) {
	var answer atomic.Bool
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), func(h *liveHarness, e providerprotocol.Event) {
		var p platforms.LivePrompt
		_ = json.Unmarshal(e.Properties, &p)
		if e.Type == "permission.asked" && answer.Load() {
			go func() {
				_ = h.adapter.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: p["sessionID"].(string), PermissionID: p["id"].(string), Reply: "once"})
			}()
		}
	}, false)
	ctx, disconnect := context.WithCancel(t.Context())
	go func() { _ = h.adapter.WatchEvents(ctx) }()
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prompt observed", func() bool { return len(got.props("permission.asked")) >= 1 })
	disconnect()
	answer.Store(true)
	go func() { _ = h.adapter.WatchEvents(t.Context()) }()
	waitFor(t, "turn idle after reconnect", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if !strings.Contains(stdinLog(t), `"behavior":"allow"`) {
		t.Fatal("prompt was not answered")
	}
}

// A subagent's prompt surfaces on the top-level session it runs under.
func TestSubagentPromptBubblesToParent(t *testing.T) {
	h, got := promptHarness(t, claudetest.Fixture("subagent.jsonl"), func(h *liveHarness, e providerprotocol.Event) {
		var p platforms.LivePrompt
		_ = json.Unmarshal(e.Properties, &p)
		if e.Type == "permission.asked" {
			go func() {
				_ = h.adapter.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: p["sessionID"].(string), PermissionID: p["id"].(string), Reply: "reject"})
			}()
		}
	})
	id := h.create(t)
	h.turn(t, id, 1)
	asked := got.props("permission.asked")
	if len(asked) != 1 || asked[0]["sessionID"] != id || asked[0]["metadata"].(map[string]any)["agentID"] != "a000000000000000d" {
		t.Fatalf("asked = %+v", asked)
	}
	if !strings.Contains(stdinLog(t), `"behavior":"deny"`) {
		t.Fatal("reject was not sent as deny")
	}
}

// An interrupt cancels the pending prompt with a cancelled reply.
func TestInterruptCancelsPrompt(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "cancel.jsonl")
	rows := `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/tmp/x"},"tool_use_id":"toolu_1"}}}
{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"interrupt"}}}
{"direction":"stdout","message":{"type":"control_cancel_request","request_id":"permission-1"}}
{"direction":"stdout","message":{"type":"result","is_error":true,"subtype":"error_during_execution"}}
`
	if err := os.WriteFile(fixturePath, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	h, got := promptHarness(t, fixturePath, nil)
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prompt", func() bool { p, _ := h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
	if p, _ := h.adapter.ListPermissions(t.Context(), id); p[0]["permission"] != "edit" || p[0]["patterns"].([]any)[0] != "/tmp/x" {
		t.Fatalf("prompt = %+v", p)
	}
	if err := h.adapter.Abort(t.Context(), platforms.AbortRequest{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	replied := got.props("permission.replied")
	if len(replied) != 1 || replied[0]["reason"] != "cancelled" {
		t.Fatalf("replied = %+v", replied)
	}
	if p, _ := h.adapter.RefreshPermissions(t.Context(), id); len(p) != 0 {
		t.Fatalf("pending after cancel = %+v", p)
	}
}

func TestToolPermission(t *testing.T) {
	for _, c := range []struct {
		tool, key, pattern string
		input              map[string]any
	}{
		{"Bash", "bash", "ls", map[string]any{"command": "ls"}},
		{"Edit", "edit", "/a", map[string]any{"file_path": "/a"}},
		{"NotebookEdit", "edit", "/n", map[string]any{"notebook_path": "/n"}},
		{"Read", "read", "*", nil},
		{"Glob", "glob", `\*.go`, map[string]any{"pattern": "*.go"}}, // an argument is exact
		{"Grep", "grep", "x", map[string]any{"pattern": "x"}},
		{"WebFetch", "webfetch", "u", map[string]any{"url": "u"}},
		{"WebSearch", "websearch", "q", map[string]any{"query": "q"}},
		{"Agent", "task", "general", map[string]any{"subagent_type": "general"}},
		{"Skill", "skill", "s", map[string]any{"skill": "s"}},
		{"TodoWrite", "todowrite", "*", nil},
		{"mcp__ocman__sessions", "mcp__ocman__sessions", "*", nil},
	} {
		key, patterns := toolPermission(c.tool, c.input)
		if key != c.key || patterns[0] != c.pattern {
			t.Errorf("%s = %s %v", c.tool, key, patterns)
		}
	}
}

type brokenStdin struct{}

func (brokenStdin) Write([]byte) (int, error) { return 0, os.ErrClosed }
func (brokenStdin) Close() error              { return nil }

// A reply claude never received is not reported as replied: the prompt stays
// pending and answerable, and a retry completes the turn.
func TestFailedReplyWriteKeepsPromptPending(t *testing.T) {
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), nil)
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prompt", func() bool { p, _ := h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
	p, _ := h.adapter.ListPermissions(t.Context(), id)
	req := platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"}
	h.live.mu.Lock()
	proc := h.live.procs[id]
	stdin := proc.stdin
	proc.stdin = brokenStdin{}
	h.live.mu.Unlock()
	if err := h.adapter.RespondPermission(t.Context(), req); err == nil {
		t.Fatal("a failed write must fail the reply")
	}
	if pending, _ := h.adapter.RefreshPermissions(t.Context(), id); len(pending) != 1 || len(got.props("permission.replied")) != 0 {
		t.Fatalf("pending = %+v replied = %+v", pending, got.props("permission.replied"))
	}
	h.live.mu.Lock()
	proc.stdin = stdin
	h.live.mu.Unlock()
	if err := h.adapter.RespondPermission(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if replied := got.props("permission.replied"); len(replied) != 1 || replied[0]["reply"] != "once" {
		t.Fatalf("replied = %+v", replied)
	}
}

// A decision claude received but whose reply event could not be spooled is
// not acknowledged as durable: the caller gets an error, the prompt stays
// listed, and it is recorded later (the same answer retried, or the
// background retry) without a second control_response reaching claude.
func TestUnrecordedReplyIsRetriedNotRedelivered(t *testing.T) {
	for _, retry := range []string{"answer", "background"} {
		t.Run(retry, func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "gate")
			h, got := promptHarness(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`, gate)), nil)
			id := h.create(t)
			h.send(t, id, "go")
			waitFor(t, "prompt", func() bool { p, _ := h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
			p, _ := h.adapter.ListPermissions(t.Context(), id)
			req := platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"}
			restore := h.spoolDown(t, id)
			if err := h.adapter.RespondPermission(t.Context(), req); err == nil {
				t.Fatal("an unrecorded reply was acknowledged as durable")
			}
			if pending := h.live.pendingPrompts(id, false); len(pending) != 1 {
				t.Fatalf("the unrecorded prompt vanished: %+v", pending)
			}
			restore()
			if retry == "answer" {
				if err := h.adapter.RespondPermission(t.Context(), req); err != nil {
					t.Fatal(err)
				}
			}
			waitFor(t, "reply recorded", func() bool { return len(got.props("permission.replied")) == 1 })
			if r := got.props("permission.replied")[0]; r["reply"] != "once" {
				t.Fatalf("recorded %+v, want the decision claude received", r)
			}
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
			if n := strings.Count(stdinLog(t), `"control_response"`); n != 1 {
				t.Fatalf("claude received %d decisions, want 1", n)
			}
			if pending := h.live.pendingPrompts(id, false); len(pending) != 0 {
				t.Fatalf("a recorded prompt stayed pending: %+v", pending)
			}
			if !drainIdle(t, h) {
				t.Fatal("a recorded reply left the supervisor undrainable")
			}
		})
	}
}
