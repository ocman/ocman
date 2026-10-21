package claudeprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/platformtest"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

// spoolDown blocks appends even as root, retaining the recorded spool for restore.
func (h *liveHarness) spoolDown(t *testing.T, id string) func() {
	t.Helper()
	spool := h.spoolPath(id)
	saved := filepath.Join(t.TempDir(), "spool.jsonl")
	if err := os.Link(spool, saved); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			if err := os.Rename(saved, spool); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Cleanup(restore)
	// ponytail: a directory target fails OpenFile for every UID; both swaps
	// are atomic so a retry cannot create a new spool in a missing-path gap.
	blocked := filepath.Join(filepath.Dir(saved), "blocked")
	if err := os.Symlink(filepath.Dir(saved), blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(blocked, spool); err != nil {
		t.Fatal(err)
	}
	return restore
}

func TestSpoolDownBlocksAppendAndPreservesRecords(t *testing.T) {
	h := &liveHarness{dir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(h.dir, "spool"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := h.spoolPath("s")
	want := "recorded before outage\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		inflight, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		restore := h.spoolDown(t, "s")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			t.Fatal("fault injection still allows spool append")
		}
		// An append opened before the outage must survive the path swaps too.
		if _, err := inflight.WriteString("in-flight record\n"); err != nil {
			t.Fatal(err)
		}
		if err := inflight.Close(); err != nil {
			t.Fatal(err)
		}
		want += "in-flight record\n"
		restore()
		restore() // explicit restore and test cleanup may both run
		if got := string(must(os.ReadFile(path))); got != want {
			t.Fatalf("restored spool = %q, want %q", got, want)
		}
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("after restore\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		want += "after restore\n"
	}
}

// oneAdapter is the session service's registry for one adapter, leaving
// the harness's lifecycle sink in place.
// providerPlatform presents the socket adapter where application code takes a
// Platform; persisted reads are not exercised by these protocol tests.
type providerPlatform struct {
	*provider.Adapter
	platformtest.NoBrowsing
}

type oneAdapter struct{ a platforms.Platform }

func (r oneAdapter) Get(id platforms.ID) (platforms.Platform, bool) { return r.a, id == r.a.ID() }
func (r oneAdapter) Platforms() []platforms.Platform                { return []platforms.Platform{r.a} }
func (r oneAdapter) PlatformForSession(context.Context, string) (platforms.Platform, bool) {
	return r.a, true
}

func setRecordRetry(t *testing.T, d time.Duration) {
	old := recordRetry.Swap(int64(d))
	t.Cleanup(func() { recordRetry.Store(old) })
}

// replyHooks records the session service's success hooks, wired as the
// server wires them: a successful reply is the user's decision.
type replyHooks struct {
	mu      sync.Mutex
	replies []string
}

func (r *replyHooks) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.replies...)
}

const askMake = `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make *"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`

// A retry that conflicts with the decision claude already received (but
// that could not be recorded) is refused through the session service: no
// success hook runs for it, and the rules and recorded provenance follow the
// decision claude actually received. The same decision repeated succeeds.
func TestConflictingReplyRetryIsRefused(t *testing.T) {
	for _, c := range []struct{ first, conflicting string }{{"always", "reject"}, {"reject", "always"}, {"once", "always"}} {
		t.Run(c.first, func(t *testing.T) {
			setRecordRetry(t, time.Hour) // only the caller retries
			st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			aa := autoapprove.NewService(autoapprove.Deps{Store: st})
			h, got := promptHarness(t, writeFixture(t, askMake), func(h *liveHarness, e providerprotocol.Event) {
				aa.HandleProviderEvent(t.Context(), providerPlatform{Adapter: h.adapter}, e)
			})
			registry := oneAdapter{providerPlatform{Adapter: h.adapter}}
			hooks := &replyHooks{}
			svc := sessionsvc.New(registry, sessionsvc.Hooks{PermissionReplySucceeded: func(ctx context.Context, _ platforms.ID, req platforms.RespondPermissionRequest) {
				hooks.mu.Lock()
				hooks.replies = append(hooks.replies, req.Reply)
				hooks.mu.Unlock()
				aa.HandleDirectPermissionReply(ctx, req.SessionID, req.PermissionID, req.Reply)
			}})
			id := h.create(t)
			h.send(t, id, "go")
			var p []platforms.LivePrompt
			waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
			reply := func(r string) error {
				return svc.RespondPermission(t.Context(), string(PlatformID), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: r})
			}
			restore := h.spoolDown(t, id)
			if err := reply(c.first); err == nil {
				t.Fatal("an unrecorded reply was acknowledged")
			}
			if err := reply(c.conflicting); !errors.Is(err, platforms.ErrUpstreamRejected) {
				t.Fatalf("conflicting retry = %v, want refused", err)
			}
			restore()
			if err := reply(c.conflicting); !errors.Is(err, platforms.ErrUpstreamRejected) {
				t.Fatalf("conflicting retry after restore = %v, want refused", err)
			}
			if n := len(hooks.got()); n != 0 {
				t.Fatalf("success hooks ran for undelivered decisions: %v", hooks.got())
			}
			if err := reply(c.first); err != nil {
				t.Fatalf("repeating the delivered decision = %v", err)
			}
			waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
			waitFor(t, "reply recorded", func() bool { return len(got.props("permission.replied")) > 0 })
			if r := got.props("permission.replied"); len(r) != 1 || r[0]["reply"] != c.first {
				t.Fatalf("recorded %+v, want %s", r, c.first)
			}
			if hooks := hooks.got(); len(hooks) != 1 || hooks[0] != c.first {
				t.Fatalf("success hooks = %v, want [%s]", hooks, c.first)
			}
			if n := strings.Count(stdinLog(t), `"control_response"`); n != 1 {
				t.Fatalf("claude received %d decisions, want 1", n)
			}
			// Rules: only an effective "always" grants (exactly) make *.
			rules, err := os.ReadFile(filepath.Join(h.dir, "rules.json"))
			if err != nil {
				t.Fatal(err)
			}
			if granted := strings.Contains(string(rules), `"approved":{"`+id); granted != (c.first == "always") {
				t.Fatalf("rules after %s/%s = %s", c.first, c.conflicting, rules)
			}
			if c.first != "always" && strings.Contains(string(rules), "make") {
				t.Fatalf("an undelivered always left a rule: %s", rules)
			}
			var approved []state.ApprovedPermission
			waitFor(t, "provenance", func() bool {
				approved, _ = st.ListApprovedPermissions(t.Context(), PlatformID, id)
				return c.first == "reject" || len(approved) == 1
			})
			time.Sleep(50 * time.Millisecond)
			approved, _ = st.ListApprovedPermissions(t.Context(), PlatformID, id)
			switch {
			case c.first == "reject" && len(approved) != 0:
				t.Fatalf("a rejected call was recorded as approved: %+v", approved)
			case c.first != "reject" && (len(approved) != 1 || string(approved[0].Reply) != c.first || approved[0].ApprovedBy != state.ApprovalActorUser):
				t.Fatalf("provenance = %+v, want user %s", approved, c.first)
			}
		})
	}
}

// Questions follow the same rule: a different answer, or a dismissal, after
// an answer claude received is refused; the same answer is recorded.
func TestConflictingQuestionRetryIsRefused(t *testing.T) {
	setRecordRetry(t, time.Hour)
	h, got := promptHarness(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"q1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"Which?","header":"H","options":[{"label":"A"},{"label":"B"}],"multiSelect":false}]},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"q1"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`), nil)
	registry := oneAdapter{providerPlatform{Adapter: h.adapter}}
	svc := sessionsvc.New(registry, sessionsvc.Hooks{})
	id := h.create(t)
	h.send(t, id, "go")
	var q []platforms.LivePrompt
	waitFor(t, "question", func() bool { q, _ = h.adapter.ListQuestions(t.Context(), id); return len(q) == 1 })
	qid := q[0]["id"].(string)
	answer := func(a string) error {
		return svc.RespondQuestion(t.Context(), string(PlatformID), platforms.RespondQuestionRequest{SessionID: id, RequestID: qid, Answers: [][]string{{a}}})
	}
	restore := h.spoolDown(t, id)
	if err := answer("A"); err == nil {
		t.Fatal("an unrecorded answer was acknowledged")
	}
	restore()
	if err := answer("B"); !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Fatalf("different answer = %v, want refused", err)
	}
	if err := svc.RejectQuestion(t.Context(), string(PlatformID), platforms.RejectQuestionRequest{SessionID: id, RequestID: qid}); !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Fatalf("dismissal = %v, want refused", err)
	}
	if err := answer("A"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	waitFor(t, "answer recorded", func() bool { return len(got.props("question.replied")) > 0 })
	if r := got.props("question.replied"); len(r) != 1 || len(got.props("question.rejected")) != 0 {
		t.Fatalf("replied = %+v", got.events)
	}
	if !strings.Contains(stdinLog(t), `"answers":{"Which?":"A"}`) || strings.Count(stdinLog(t), `"control_response"`) != 1 {
		t.Fatalf("stdin = %s", stdinLog(t))
	}
}
