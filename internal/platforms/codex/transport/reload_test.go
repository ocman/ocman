package codextransport

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/testutil"
)

func TestCodexReloadIdleUsesAuthoritativeSupervisorInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		row    db.Session
		idle   bool
		prompt string
	}{
		{"acknowledged-active-turn", db.Session{ID: "thr", Status: db.StatusBusy}, false, ""},
		{"permission", db.Session{ID: "thr", PendingPermission: true}, false, ""},
		{"question", db.Session{ID: "thr", PendingQuestion: true}, false, ""},
		{"limited", db.Session{ID: "thr", Status: db.StatusLimited}, false, ""},
		{"completed-unread", db.Session{ID: "thr", Status: db.StatusWaiting}, true, ""},
		{"permission-without-runtime-flag", db.Session{ID: "thr", Status: db.StatusWaiting}, false, "/permissions"},
		{"question-without-runtime-flag", db.Session{ID: "thr", Status: db.StatusWaiting}, false, "/questions"},
		{"prompt-read-error", db.Session{ID: "thr", Status: db.StatusWaiting}, false, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			p := r.platform(t.Context())
			reader, ok := any(p).(platforms.ReloadIdleReader)
			if !ok {
				t.Fatal("Codex lacks authoritative ReloadIdle; acknowledged turns fall back to history")
			}
			socket := filepath.Join(r.data, "idle.sock")
			l, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/hello":
					_ = json.NewEncoder(w).Encode(providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "codex", CLIVersion: "test-only", Capabilities: providerprotocol.Capabilities{Capabilities: platforms.Capabilities{RespondPermission: true, RespondQuestion: true}}})
				case "/sessions":
					if req.URL.Query().Get("runtime") != "1" {
						t.Error("reload requested history instead of runtime inventory")
					}
					_ = json.NewEncoder(w).Encode([]db.Session{tc.row})
				case "/permissions", "/questions":
					if tc.prompt == "error" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					prompts := []platforms.LivePrompt{}
					if tc.prompt == req.URL.Path {
						prompts = append(prompts, platforms.LivePrompt{"id": "prompt", "sessionID": "thr"})
					}
					_ = json.NewEncoder(w).Encode(prompts)
				default:
					t.Errorf("unexpected reload call %s", req.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})}
			go func() { _ = srv.Serve(l) }()
			t.Cleanup(func() { _ = srv.Close() })
			if _, err := p.attach(t.Context(), "unix://"+socket); err != nil {
				t.Fatal(err)
			}
			idle, err := reader.ReloadIdle(t.Context())
			if (err != nil) != (tc.prompt == "error") || idle != tc.idle {
				t.Fatalf("idle=%v err=%v", idle, err)
			}
			_ = srv.Close()
			if idle, err := reader.ReloadIdle(t.Context()); err == nil || idle {
				t.Fatalf("disconnected supervisor treated as idle: %v %v", idle, err)
			}
		})
	}
}

func TestCodexReloadIdleFencesAcknowledgedSendAndUnattachedSupervisor(t *testing.T) {
	r := newRig(t)
	t.Setenv("FAKE_CODEX_APPROVAL", "1")
	p := r.platform(t.Context())
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID, Message: "approval remains after send acknowledges"}); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, 5*time.Second, "fake Codex approval", func() bool {
		prompts, err := p.RefreshPermissions(t.Context(), created.ID)
		return err == nil && len(prompts) != 0
	})
	reader, ok := any(p).(platforms.ReloadIdleReader)
	if !ok {
		t.Fatal("acknowledged Codex send has no authoritative idle reader")
	}
	if idle, err := reader.ReloadIdle(t.Context()); err != nil || idle {
		t.Fatalf("pending acknowledged turn idle=%v err=%v", idle, err)
	}
	// Simulate restart before background recovery attaches. Persistent native
	// ownership, rather than browser history or a request lease, must hold it.
	p.ensureMu.Lock()
	p.mu.Lock()
	a := p.adapter
	p.adapter = nil
	p.mu.Unlock()
	p.ensureMu.Unlock()
	t.Cleanup(a.Close)
	if idle, err := reader.ReloadIdle(t.Context()); err != nil || idle {
		t.Fatalf("unattached live turn idle=%v err=%v", idle, err)
	}
	row, exists, err := r.store.GetManagedProvider(t.Context(), "codex", p.root)
	if err != nil || !exists {
		t.Fatalf("persisted supervisor=%+v %v", row, err)
	}
	row.Endpoint = "unix://" + filepath.Join(r.data, "unreadable")
	if err := r.store.UpsertManagedProvider(t.Context(), "codex", p.root, row); err != nil {
		t.Fatal(err)
	}
	if idle, err := reader.ReloadIdle(t.Context()); err == nil || idle {
		t.Fatalf("unreadable live owner idle=%v err=%v", idle, err)
	}
	r.killSupervisors()
	if idle, err := reader.ReloadIdle(t.Context()); err != nil || !idle {
		t.Fatalf("confirmed stopped owner idle=%v err=%v", idle, err)
	}
}

func TestCodexReloadIdleFailsClosedWithoutOwnershipOrDuringDrain(t *testing.T) {
	reader, ok := any(&Platform{}).(platforms.ReloadIdleReader)
	if !ok {
		t.Fatal("Codex has no authoritative idle reader")
	}
	if idle, err := reader.ReloadIdle(t.Context()); err == nil || idle {
		t.Fatalf("missing ownership idle=%v err=%v", idle, err)
	}
	r := newRig(t)
	p := r.platform(t.Context())
	p.mu.Lock()
	p.draining = true
	p.mu.Unlock()
	reader = p
	if idle, err := reader.ReloadIdle(t.Context()); err == nil || idle {
		t.Fatalf("draining owner idle=%v err=%v", idle, err)
	}
}
