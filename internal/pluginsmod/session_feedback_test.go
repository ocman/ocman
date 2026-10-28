package pluginsmod

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/worker"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestSessionPumpPassesPausedHeads(t *testing.T) {
	db := openStore(t)
	m := NewModule(Deps{Store: db})
	d := plugins.Description{ID: "org.example.enabled", Name: "Enabled", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Capabilities: []plugins.Capability{plugins.SessionCapability}, RequestedGrants: []string{plugins.SessionRunGrant},
		Settings: []plugins.Setting{{Key: "project", Label: "Project", Type: "string", Required: true}}}
	if err := db.DiscoverPlugin(t.Context(), d, "/plugin", strings.Repeat("a", 64), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginEnabled(t.Context(), d.ID, true, d.RequestedGrants); err != nil {
		t.Fatal(err)
	}
	disabled := d
	disabled.ID = "org.example.disabled"
	if err := db.DiscoverPlugin(t.Context(), disabled, "/disabled-plugin", strings.Repeat("b", 64), nil); err != nil {
		t.Fatal(err)
	}
	for i := range 32 {
		if _, err := m.plugins.AppendPluginSessionCompletion(t.Context(), "org.example.disabled", "opencode", fmt.Sprint(i), fmt.Sprint(i), "paused"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.plugins.AppendPluginSessionCompletion(t.Context(), d.ID, "opencode", "enabled", "enabled", "ready"); err != nil {
		t.Fatal(err)
	}
	m.sessionOnce.Do(m.buildSessions)
	m.sessionDeliveries.Close()
	seen := make(chan string, 33)
	m.sessionDeliveries = worker.NewKeyed(func(c PluginSessionCompletion) { seen <- c.SessionID; m.sessionOutbox.end(c.ID) }, PluginSessionCompletion.Group)
	t.Cleanup(m.sessionJobs.Close)
	t.Cleanup(m.sessionDeliveries.Close)
	m.sessionOutbox.pump(t.Context())
	if err := m.sessionDeliveries.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-seen:
		if id != "enabled" {
			t.Fatal(id)
		}
	default:
		t.Fatal("paused heads starved enabled recipient")
	}
	var pending, attempts int
	if err := m.plugins.db.QueryRow(`SELECT count(*),sum(attempts) FROM plugin_session_outbox WHERE plugin_id=? AND status='pending'`, disabled.ID).Scan(&pending, &attempts); err != nil || pending != 32 || attempts != 0 {
		t.Fatalf("paused rows changed: pending=%d attempts=%d err=%v", pending, attempts, err)
	}
	if err := db.SetPluginEnabled(t.Context(), disabled.ID, true, disabled.RequestedGrants); err != nil {
		t.Fatal(err)
	}
	m.sessionOutbox.pump(t.Context())
	if err := m.sessionDeliveries.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 32 {
		t.Fatalf("re-enabled rows not resumed within batch budget: %d", len(seen))
	}
}

func TestSessionRunReusesDurableResult(t *testing.T) {
	db := openStore(t)
	sessions := &stubSessions{}
	prompts := 0
	deps := Deps{Store: db, Local: func() hostsvc.Host { return stubHost{} }, Sessions: sessions,
		Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error { prompts++; return nil }}
	cfg := plugins.Config{Project: t.TempDir()}
	p := plugins.SessionRunParams{Prompt: "once"}
	for range 2 {
		m := NewModule(deps) // retries survive a module reload
		id, err := m.startSession(t.Context(), "org.example.run", cfg, p, cfg.Project, "run-1")
		if err != nil || id != "ses-chat" {
			t.Fatalf("run: %q %v", id, err)
		}
	}
	if len(sessions.created) != 1 || prompts != 1 {
		t.Fatalf("created %d sessions, %d prompts", len(sessions.created), prompts)
	}
	p.Prompt = "changed"
	_, err := NewModule(deps).startSession(t.Context(), "org.example.run", cfg, p, cfg.Project, "run-1")
	if wire := plugins.RequestError(err); wire.Category != plugins.ErrorConflict {
		t.Fatalf("conflicting retry: %v", err)
	}
}

func TestRunJoinsSessionWorkers(t *testing.T) {
	for _, kind := range []string{"run", "delivery"} {
		t.Run(kind, func(t *testing.T) {
			m := NewModule(Deps{})
			m.sessionOnce.Do(m.buildSessions)
			entered, release := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() { close(release); m.sessionJobs.Close(); m.sessionDeliveries.Close() })
			authorize := func(_ context.Context, _ string, use func(plugins.Description, []string, plugins.Config) error) error {
				return use(plugins.Description{Capabilities: []plugins.Capability{plugins.SessionCapability}}, []string{plugins.SessionRunGrant}, plugins.Config{Project: "/repo"})
			}
			m.sessionBroker = plugins.NewSessionBroker(authorize, func(context.Context, string, plugins.Config, plugins.SessionRunParams, string, string) (string, error) {
				close(entered)
				<-release
				return "ses-1", nil
			}, func(context.Context, string, plugins.Call) (<-chan plugins.Reply, error) {
				close(entered)
				<-release
				replies := make(chan plugins.Reply, 1)
				replies <- plugins.Reply{Message: plugins.Envelope{Type: plugins.TypeResult, Result: &plugins.Result{Value: json.RawMessage(`{}`)}}}
				return replies, nil
			})
			if kind == "run" {
				m.sessionJobs.Close()
				m.sessionJobs = worker.NewKeyed(func(j sessionJob) { _, _ = m.SessionBroker().Run(j.request.Context, j.pluginID, j.request.Call) }, func(j sessionJob) string { return j.pluginID })
				m.sessionJobs.Enqueue(sessionJob{pluginID: "p", request: plugins.Request{Context: t.Context(), Call: plugins.Call{OperationID: "op-1", Capability: "session", Version: plugins.Version{Major: 1}, Method: "run", Params: json.RawMessage(`{"prompt":"go"}`)}}})
			} else {
				// Use the real keyed worker, with a blocked completion call.
				m.sessionDeliveries.Close()
				m.sessionDeliveries = worker.NewKeyed(func(c PluginSessionCompletion) {
					_ = m.SessionBroker().Completed(t.Context(), "p", "op-1", plugins.SessionCompleted{SessionID: "ses-1", Text: "done"})
				}, PluginSessionCompletion.Group)
				m.sessionDeliveries.Enqueue(PluginSessionCompletion{SessionID: "ses-1"})
			}
			<-entered
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			done := make(chan struct{})
			go func() { m.Run(ctx); close(done) }()
			select {
			case <-done:
				t.Fatal("Run returned while session work was active")
			case <-time.After(30 * time.Millisecond):
			}
		})
	}
}
