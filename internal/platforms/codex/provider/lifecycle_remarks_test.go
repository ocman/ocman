package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/platforms/codex/appserver/codextest"
)

func TestRemarksReplacementRetiresBeforeWatcher(t *testing.T) {
	h := newHarness(t, scripted)
	c, _, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// This backend owns a captured generation without an exit watcher.
	// Reconnection must retire it itself, regardless of watcher scheduling.
	b := &backend{ctx: h.b.ctx, opts: h.b.opts, sup: h.b.sup, client: c, gen: 1,
		loaded: map[string]bool{"thr": true}, sessions: map[string]*session{"thr": {running: true, accepted: 7}}, prompts: map[string]*prompt{}}
	_ = c.Close()
	_ = c.Notify("force-closed-write", nil)
	<-c.Done()
	next, gen, err := b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	if gen != 2 || b.sessions["thr"].running {
		t.Fatalf("replacement skipped retirement: gen=%d running=%v", gen, b.sessions["thr"].running)
	}
	b.mu.Lock()
	b.sessions["thr"].running = true
	b.sessions["thr"].accepted = 8
	b.mu.Unlock()
	b.lost(1, errors.New("late watcher"))
	b.mu.Lock()
	running := b.sessions["thr"].running
	b.mu.Unlock()
	if !running {
		t.Fatal("old watcher settled replacement turn")
	}
}

func TestRemarksStaleThreadResponses(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(strconv.FormatBool(create), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			h := newHarness(t, func(s *codextest.Server) {
				scripted(s)
				method := codexappserver.MethodThreadResume
				if create {
					method = codexappserver.MethodThreadStart
				}
				s.Handle(method, func(json.RawMessage) (any, error) {
					close(entered)
					<-release
					return map[string]any{"thread": thread("thr"), "cwd": "/repo"}, nil
				})
			})
			c, gen, err := h.b.conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if create {
					_, err := h.b.create(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
					done <- err
				} else {
					done <- h.b.load(t.Context(), c, gen, "thr")
				}
			}()
			<-entered
			h.b.lost(gen, errors.New("connection lost before response application"))
			close(release)
			if err := <-done; err == nil || errors.Is(err, platforms.ErrCreationRejected) {
				t.Fatalf("stale response = %v", err)
			}
		})
	}
}

func TestRemarksFinalUpsertRecovery(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	restore := breakSpool(t, h)
	h.b.item("thr", codexappserver.Item{ID: "a", Type: "agentMessage", Text: "retained"})
	h.b.turnCompleted("thr", &codexappserver.Turn{ID: "t1", Status: codexappserver.TurnCompleted})
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	if !running {
		restore()
		t.Fatal("idle overtook failed upsert")
	}
	restore()
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "retained" })
	h.next("session.idle", nil)
	found := false
	for _, event := range h.b.snapshot("thr") {
		if event.Type == "message.part.updated" {
			found = true
		}
	}
	if !found {
		t.Fatal("recovered snapshot lost final part")
	}
}

func TestRemarksHistoryMetadataRace(t *testing.T) {
	h := newHarness(t, scripted)
	if _, err := h.b.create(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				h.b.mu.Lock()
				h.b.sessions["thr"].title = "renamed"
				h.b.mu.Unlock()
			}
		}
	})
	for range 100 {
		if _, err := h.b.history(t.Context(), "thr"); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestRemarksExitedProcessReaped(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, b, err := newSupervisor(ctx, Options{CLI: exe, Env: append(os.Environ(), "FAKE_CODEX=exit")}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		PID int `json:"pid"`
	}
	if err := c.Call(t.Context(), "test/exit", nil, &result); err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(syscall.Kill(result.PID, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatal("spawned app-server was not reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemarksModelOverride(t *testing.T) {
	h := newHarness(t, scripted)
	if err := h.b.send(t.Context(), "thr", platforms.SendMessageRequest{Message: "hi", Model: "other/custom"}); err != nil {
		t.Fatal(err)
	}
	h.b.mu.Lock()
	c := h.b.sessions["thr"].conv
	h.b.mu.Unlock()
	if c.ProviderID != "other" || c.ModelID != "custom" {
		t.Fatalf("override = %+v", c)
	}
	h.b.settle("thr", db.StatusDone, 1)
}
