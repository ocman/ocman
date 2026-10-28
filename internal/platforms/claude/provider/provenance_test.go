package claudeprovider

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/permissions"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
)

// spoolDownOnReply takes the spool down just before the pipeline's reply
// reaches claude, once armed: claude receives it, the supervisor cannot
// record it.
type spoolDownOnReply struct {
	*provider.Adapter
	platformtest.NoBrowsing
	down func()
}

func (s spoolDownOnReply) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	s.down()
	return s.Adapter.RespondPermission(ctx, req)
}

// The judge (or the safe-command cache) approves; claude receives the
// approval but its event is recorded only later. The approval stays the
// AI's: the delayed permission.replied, and its replay to a restarted
// pipeline, never turn it into a user "once" (which children would not
// inherit) nor drop its audit row.
func TestDelayedAIApprovalKeepsProvenance(t *testing.T) {
	for _, path := range []string{"judge", "cache"} {
		t.Run(path, func(t *testing.T) {
			fastRetries(t)
			st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if err := st.SetJudgeDelayMs(t.Context(), 0); err != nil {
				t.Fatal(err)
			}
			// The prewarmed fake answers the judge call: a fresh shell
			// script's first exec can stall seconds under the suite's
			// parallel load, which used to blow the harness' waits.
			t.Setenv("FAKE_CLAUDE_PRINT", `{"verdict":"safe","reasoning":"Builds."}`)
			cli := claudetest.Binary(t)
			var id atomic.Value
			var armed atomic.Bool
			restores := make(chan func(), 1)
			var h *liveHarness
			aa := autoapprove.NewService(autoapprove.Deps{Store: st, OpenCodeDisabled: true, DefaultEnabled: true,
				FallbackJudge: autoapprove.ClaudeJudge{CLI: cli},
				SessionDir:    func(string) (string, error) { return h.dir, nil }})
			h, got := promptHarness(t, writeFixture(t, askMake), func(h *liveHarness, e providerprotocol.Event) {
				aa.HandleProviderEvent(t.Context(), spoolDownOnReply{Adapter: h.adapter, down: func() {
					if armed.CompareAndSwap(true, false) {
						restores <- h.spoolDown(t, id.Load().(string))
					}
				}}, e)
			})
			session := h.create(t)
			id.Store(session)
			approvals := func() []state.ApprovedPermission {
				a, _ := st.ListApprovedPermissions(t.Context(), PlatformID, session)
				return a
			}
			if path == "cache" { // the judge approves the first call; the cache the second
				h.turn(t, session, 1)
				waitFor(t, "judge approval", func() bool { return len(approvals()) == 1 })
			}
			armed.Store(true)
			before := len(approvals())
			waitFor(t, "previous child stopped", func() bool { return h.procs() == 0 })
			h.send(t, session, "go")
			waitFor(t, "AI approval recorded", func() bool { return len(approvals()) == before+1 })
			(<-restores)()
			waitFor(t, "delayed reply recorded", func() bool { return len(got.props("permission.replied")) == before+1 })
			time.Sleep(200 * time.Millisecond) // a user capture would land now

			// A restarted pipeline replays the ask and its reply.
			spy := &lifecycleSpy{DB: st}
			restarted := autoapprove.NewService(autoapprove.Deps{Store: spy})
			var emitted bytes.Buffer
			restarted.RegisterSink(session, &emitted, nil)
			got.mu.Lock()
			events := slices.Clone(got.events)
			got.mu.Unlock()
			for _, typ := range []string{"permission.asked", "permission.replied"} {
				for _, e := range events {
					if e.Type == typ {
						restarted.HandleProviderEvent(t.Context(), providerPlatform{Adapter: h.adapter}, e)
					}
				}
			}
			time.Sleep(200 * time.Millisecond)
			// The replay reports the AI's approval to a browser, whichever
			// arrives first: this event or the persisted notice (same key).
			if strings.Contains(emitted.String(), "ocman.permission.approved") || !strings.Contains(emitted.String(), `"approvedBy":"ai"`) {
				t.Fatalf("replay emitted %s", emitted.String())
			}
			for _, l := range spy.updates() {
				if l.Resolution != "" && l.Resolution != state.PermissionResolutionAutoApproved {
					t.Fatalf("replay recorded lifecycle %+v", l)
				}
			}
			rows := approvals()
			if len(rows) != before+1 {
				t.Fatalf("approvals = %+v", rows)
			}
			for _, r := range rows {
				if r.ApprovedBy != state.ApprovalActorAI || r.Reply != state.ApprovalReplyOnce {
					t.Fatalf("an AI approval was attributed to %s/%s", r.ApprovedBy, r.Reply)
				}
			}
			if path == "cache" && rows[len(rows)-1].Reasoning[:7] != "cached:" {
				t.Fatalf("the second approval did not come from the cache: %+v", rows[len(rows)-1])
			}
			inherited, _, err := permissions.BuildInheritedRules(t.Context(), st, PlatformID, session)
			if err != nil || len(inherited) != 1 || inherited[0].Permission != "bash" {
				t.Fatalf("judge-safe approval not inherited: %+v %v", inherited, err)
			}
		})
	}
}

// lifecycleSpy records lifecycle updates on top of the real store.
type lifecycleSpy struct {
	*state.DB
	mu  sync.Mutex
	ups []state.PermissionLifecycle
}

func (l *lifecycleSpy) UpsertPermissionLifecycle(ctx context.Context, u state.PermissionLifecycle) error {
	l.mu.Lock()
	l.ups = append(l.ups, u)
	l.mu.Unlock()
	return l.DB.UpsertPermissionLifecycle(ctx, u)
}

func (l *lifecycleSpy) updates() []state.PermissionLifecycle {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ups)
}

// legacyReplies is a draining version-1 supervisor: a reply claude received
// but could not record fails with a generic 503, not ErrReplyUnrecorded.
type legacyReplies struct{ spoolDownOnReply }

func (l legacyReplies) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	if err := l.spoolDownOnReply.RespondPermission(ctx, req); err != nil {
		return errors.New("provider HTTP 503: spool unavailable")
	}
	return nil
}

func (legacyReplies) ReplyOutcomes(context.Context, string) bool { return false }

// Behind a supervisor that cannot report a delivered-but-unrecorded reply,
// the judge's approval could reach claude while the pipeline sees a plain
// failure, and the delayed permission.replied would read as the user's
// "once". The pipeline leaves such prompts to the user instead.
func TestLegacySupervisorLeavesApprovalToUser(t *testing.T) {
	fastRetries(t)
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetJudgeDelayMs(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_PRINT", `{"verdict":"safe","reasoning":"Builds."}`)
	cli := claudetest.Binary(t)
	var id atomic.Value
	id.Store("")
	var restore atomic.Pointer[func()]
	var h *liveHarness
	aa := autoapprove.NewService(autoapprove.Deps{Store: st, OpenCodeDisabled: true, DefaultEnabled: true,
		FallbackJudge: autoapprove.ClaudeJudge{CLI: cli},
		SessionDir:    func(string) (string, error) { return h.dir, nil }})
	h, _ = promptHarness(t, writeFixture(t, askMake), func(h *liveHarness, e providerprotocol.Event) {
		aa.HandleProviderEvent(t.Context(), legacyReplies{spoolDownOnReply{Adapter: h.adapter, down: func() {
			if restore.Load() == nil {
				r := h.spoolDown(t, id.Load().(string))
				restore.Store(&r)
			}
		}}}, e)
	})
	session := h.create(t)
	id.Store(session)
	h.send(t, session, "go")
	waitFor(t, "prompt left to the user", func() bool {
		p, _ := h.adapter.ListPermissions(t.Context(), session)
		return len(p) == 1
	})
	time.Sleep(300 * time.Millisecond) // the judge has decided by now
	if r := restore.Load(); r != nil {
		(*r)()
	}
	time.Sleep(300 * time.Millisecond) // a delayed reply would be recorded now
	rows, _ := st.ListApprovedPermissions(t.Context(), PlatformID, session)
	for _, r := range rows {
		t.Fatalf("an approval nobody gave the user was recorded: %+v", r)
	}
	if p, _ := h.adapter.ListPermissions(t.Context(), session); len(p) != 1 {
		t.Fatalf("the prompt was answered behind the user's back: %+v", p)
	}
}
