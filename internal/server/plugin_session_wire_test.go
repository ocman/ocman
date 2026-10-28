package server

import (
	"github.com/ocman/ocman/internal/pluginsmod"

	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPluginSessionWireDenials(t *testing.T) {
	for _, test := range []struct {
		name    string
		subpath string
		profile string
		deleted bool
	}{
		{name: "symlink escape", subpath: "escape"},
		{name: "absolute", subpath: "/etc"},
		{name: "traversal", subpath: "a/../b"},
		{name: "missing profile", profile: "missing"},
		{name: "deleted profile", profile: "missing", deleted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSessionFixture(t)
			if err := os.Symlink(t.TempDir(), filepath.Join(f.project, "escape")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(f.project, "b"), 0700); err != nil {
				t.Fatal(err)
			}
			if test.deleted {
				if err := pluginsmod.NewStore(f.s.stateDB).CreatePermissionProfile(t.Context(), pluginsmod.PermissionProfile{
					ID: "p1", Name: "missing", RulesJSON: `[{"permission":"bash","pattern":"*","action":"deny"}]`,
				}); err != nil {
					t.Fatal(err)
				}
				if err := pluginsmod.NewStore(f.s.stateDB).DeletePermissionProfile(t.Context(), "p1"); err != nil {
					t.Fatal(err)
				}
			}
			f.install(t, map[string]string{"project": f.project, "prompt": "go", "subpath": test.subpath, "profile": test.profile}, []string{plugins.SessionRunGrant})
			category := "permission_denied"
			if test.profile != "" {
				category = "not_found"
			}
			f.awaitRecord(t, "run\t1\terror\t"+category+"\n")
			if len(f.createRequests()) != 0 || len(f.awaitPromptsNoWait()) != 0 {
				t.Fatal("denied wire request created a session or enqueued a prompt")
			}
		})
	}
}

func (f *sessionFixture) awaitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		p := f.s.pluginsModule().Process(sessionPluginDescription().ID)
		ready := p != nil && p.Health().Status == "ready"
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("plugin did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// reopen discards all host memory, reopens the same disposable state database,
// and discovers a fresh executable process using its saved owner approval.
func (f *sessionFixture) reopen(t *testing.T) {
	t.Helper()
	f.s.stopPluginProcesses()
	if err := f.s.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(f.statePath, ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	old := f.s
	f.s = &Server{stateDB: db, registry: old.registry, hostRouter: old.hostRouter}
	f.s.sessions = sessionsvc.New(f.s.registry, sessionsvc.Hooks{})
	bindPlugins(f.s, context.Background())
	t.Cleanup(f.s.stopPluginProcesses)
	if _, err := f.s.RescanPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.awaitReady(t)
}

func TestPluginSessionCompletionReopenReplay(t *testing.T) {
	for _, crash := range []bool{false, true} {
		name := "restart before send"
		if crash {
			name = "consumer crash after effect before ack"
		}
		t.Run(name, func(t *testing.T) {
			f := newSessionFixture(t)
			id := sessionPluginDescription().ID
			mode := ""
			if crash {
				mode = "crash-once"
			}
			f.install(t, map[string]string{"project": f.project, "completionMode": mode}, []string{plugins.SessionRunGrant})
			f.awaitReady(t)
			ctx := t.Context()
			if err := pluginsmod.NewStore(f.s.stateDB).LinkPluginSession(ctx, "opencode", "ses-run-1", id); err != nil {
				t.Fatal(err)
			}
			deliveryID, err := pluginsmod.NewStore(f.s.stateDB).AppendPluginSessionCompletion(ctx, id, "opencode", "ses-run-1", "ses-run-1:m2", "answer")
			if err != nil {
				t.Fatal(err)
			}
			operationID := "sess-out:" + strconv.FormatInt(deliveryID, 10)
			if crash {
				// Exercise the real transport but leave the host row unacknowledged,
				// exactly the durable state left by a crash in the send/ack window.
				err := f.s.pluginsModule().SessionBroker().Completed(ctx, id, operationID, plugins.SessionCompleted{SessionID: "ses-run-1", Text: "answer"})
				if !errors.Is(err, plugins.ErrUnavailable) {
					t.Fatalf("consumer did not crash before acknowledging: %v", err)
				}
				f.awaitRecord(t, "completed\tses-run-1\t"+operationID+"\tanswer\n")
			}
			f.reopen(t)
			owner, linked, err := pluginsmod.NewStore(f.s.stateDB).GetPluginSessionPlugin(ctx, "opencode", "ses-run-1")
			if err != nil || !linked || owner != id {
				t.Fatalf("lost mapping after reopen: %q %v %v", owner, linked, err)
			}
			claimed, err := pluginsmod.NewStore(f.s.stateDB).ClaimPluginSessionCompletions(ctx, 10)
			if err != nil || len(claimed) != 1 || claimed[0].ID != deliveryID {
				t.Fatalf("lost completion identity after reopen: %+v %v", claimed, err)
			}
			f.s.pluginsModule().DeliverSessionCompletion(claimed[0])
			f.awaitRecord(t, "completed\tses-run-1\t"+operationID+"\tanswer\n")
			claimed, err = pluginsmod.NewStore(f.s.stateDB).ClaimPluginSessionCompletions(ctx, 10)
			if err != nil || len(claimed) != 0 {
				t.Fatalf("replayed completion not acknowledged: %+v %v", claimed, err)
			}
			calls, err := os.ReadFile(f.record + ".calls")
			wantCalls := 1
			if crash {
				wantCalls = 2
			}
			if err != nil || string(calls) != strings.Repeat(operationID+"\n", wantCalls) || strings.Count(f.recordText(), "completed\t") != 1 {
				t.Fatalf("unstable replay identity or duplicate consumer effect: calls=%q effects=%q err=%v", calls, f.recordText(), err)
			}
			f.s.pluginsModule().ReconcileSessionCompletions(ctx)
			if strings.Count(f.recordText(), "completed\t") != 1 {
				t.Fatal("reconciliation duplicated an acknowledged effect")
			}
		})
	}
}

func TestPluginSessionInvalidWireAckRetainsCompletion(t *testing.T) {
	f := newSessionFixture(t)
	id := sessionPluginDescription().ID
	f.install(t, map[string]string{"project": f.project, "completionMode": "invalid-ack"}, []string{plugins.SessionRunGrant})
	f.awaitReady(t)
	ctx := t.Context()
	deliveryID, err := pluginsmod.NewStore(f.s.stateDB).AppendPluginSessionCompletion(ctx, id, "opencode", "ses-run-1", "ses-run-1:m2", "answer")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := pluginsmod.NewStore(f.s.stateDB).ClaimPluginSessionCompletions(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	f.s.pluginsModule().DeliverSessionCompletion(claimed[0])
	f.reopen(t)
	// A malformed success must leave the row pending with a failed attempt,
	// rather than acknowledge and lose it. It is held by the normal backoff.
	// The reopened host may be writing; wait for its lock instead of failing.
	raw, err := sql.Open("sqlite", "file:"+f.statePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var status string
	var attempts int
	if err := raw.QueryRowContext(ctx, `SELECT status,attempts FROM plugin_session_outbox WHERE id=?`, deliveryID).Scan(&status, &attempts); err != nil || status != "pending" || attempts != 1 {
		t.Fatalf("invalid ack lost pending row: %q %d %v", status, attempts, err)
	}
}
