package pluginsmod

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestToolSessionRecoversCompletedTurnWithoutIdleEvent(t *testing.T) {
	for _, workflow := range []string{"documents", "inventory"} {
		t.Run(workflow, func(t *testing.T) {
			core := openStore(t)
			p := &recoveryPlatform{followupPlatform: followupPlatform{detail: &platforms.SessionDetail{
				Session:  &db.Session{Status: db.StatusDone},
				Messages: []db.Message{{ID: "answer", Data: json.RawMessage(`{"role":"assistant","cost":0,"tokens":{"input":2,"output":3}}`)}},
				Parts:    []db.Part{{MessageID: "answer", Data: mustPartText(t, `{"result":"complete"}`)}},
			}}}
			m := NewModule(Deps{Store: core, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }})
			ctx, cancel := context.WithCancel(t.Context())
			m.Bind(ctx)
			t.Cleanup(func() { cancel(); m.workers.Close() })
			if err := m.plugins.LinkPluginSession(ctx, "platform", "session-a", workflow); err != nil {
				t.Fatal(err)
			}
			binding := openTools(t, m, workflow, "session-a", "run-a", "read-item", 2)
			// No idle edge or periodic global reconciliation is sent. The active
			// session must capture its completed turn before its tool deadline.
			select {
			case <-binding.done:
			case <-time.After(3 * time.Second):
				t.Fatal("completed tool session waits for the global reconciliation sweep")
			}
			// Join the worker before reading its outbox append. Revocation
			// happens before that append, so binding.done alone is not a receipt.
			m.workers.Close()
			rows, err := m.plugins.ClaimPluginSessionCompletions(ctx, 10)
			if err != nil || len(rows) != 1 || rows[0].Text != `{"result":"complete"}` || rows[0].Usage == nil {
				t.Fatalf("completed turn not captured durably with usage: %+v %v", rows, err)
			}
		})
	}
}

type recoveryPlatform struct {
	followupPlatform
	mu   sync.Mutex
	read chan struct{}
}

func (p *recoveryPlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	detail, err := p.followupPlatform.Session(ctx, id, limit, offset)
	if p.read != nil {
		select {
		case p.read <- struct{}{}:
		default:
		}
	}
	return detail, err
}

func (*recoveryPlatform) Abort(context.Context, platforms.AbortRequest) error { return nil }

func TestToolSessionPolicyAllowsExactMCPActionOnly(t *testing.T) {
	for _, action := range []string{"ocman_plugin_session_tool", "mcp__ocman__plugin_session_tool"} {
		allowed := false
		for _, rule := range toolSessionRules() {
			if (rule.Permission == "*" || rule.Permission == action) && rule.Pattern == "*" {
				allowed = rule.Action == "allow"
			}
		}
		if !allowed {
			t.Errorf("authorized callback is hidden by deny-all policy: %s", action)
		}
	}
	for _, action := range []string{"shell", "read", "edit", "ocman_sessions", "ocman_plugin_session_tool_extra", "other_plugin_session_tool"} {
		allowed := false
		for _, rule := range toolSessionRules() {
			if rule.Permission == "*" || rule.Permission == action {
				allowed = rule.Action == "allow"
			}
		}
		if allowed {
			t.Errorf("unrelated tool escaped deny-all policy: %s", action)
		}
	}
}

func TestToolSessionRecoveryDoesNotRevokeBeforeFirstReply(t *testing.T) {
	core := openStore(t)
	p := &recoveryPlatform{followupPlatform: followupPlatform{detail: &platforms.SessionDetail{Session: &db.Session{Status: db.StatusWaiting}}}}
	m := NewModule(Deps{Store: core, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }})
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	t.Cleanup(func() { cancel(); m.workers.Close() })
	if err := m.plugins.LinkPluginSession(ctx, "platform", "session-a", "documents"); err != nil {
		t.Fatal(err)
	}
	b := openTools(t, m, "documents", "session-a", "run-a", "read-item", 2)
	m.ReplyToSession(ctx, "platform", "session-a")
	select {
	case <-b.done:
		t.Fatal("waiting session without a reply lost its tool binding")
	default:
	}
	rows, err := m.plugins.ClaimPluginSessionCompletions(ctx, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty turn captured: %+v %v", rows, err)
	}
}

func TestToolSessionRecoveryWaitsForTerminalHistoryProjection(t *testing.T) {
	core := openStore(t)
	p := &recoveryPlatform{followupPlatform: followupPlatform{detail: &platforms.SessionDetail{Session: &db.Session{Status: db.StatusBusy}}}, read: make(chan struct{}, 1)}
	m := NewModule(Deps{Store: core, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }})
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	t.Cleanup(func() { cancel(); m.workers.Close() })
	if err := m.plugins.LinkPluginSession(ctx, "platform", "session-a", "documents"); err != nil {
		t.Fatal(err)
	}
	b := openTools(t, m, "documents", "session-a", "run-a", "read-item", 2)
	select {
	case <-p.read:
	case <-time.After(3 * time.Second):
		t.Fatal("active recovery never read session lifecycle")
	}
	select {
	case <-b.done:
		t.Fatal("busy turn revoked tools")
	default:
	}
	p.mu.Lock()
	p.detail = &platforms.SessionDetail{Session: &db.Session{Status: db.StatusDone}, Messages: []db.Message{{ID: "answer", Data: json.RawMessage(`{"role":"assistant","cost":0,"tokens":{"input":1,"output":2}}`)}}, Parts: []db.Part{{MessageID: "answer", Data: mustPartText(t, `{"result":"complete"}`)}}}
	p.mu.Unlock()
	select {
	case <-b.done:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal projection was not recovered without another idle edge")
	}
	m.workers.Close()
	rows, err := m.plugins.ClaimPluginSessionCompletions(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("missing terminal completion: %+v %v", rows, err)
	}
	m.ReplyToSession(ctx, "platform", "session-a")
	rows, err = m.plugins.ClaimPluginSessionCompletions(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("late idle edge duplicated the completion: %+v %v", rows, err)
	}
}
