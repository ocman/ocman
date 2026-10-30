package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func TestNativeReaderReplacementPreservesACPAdmission(t *testing.T) {
	h := newHarness(t, scripted)
	c, _, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.start("thr")
	_ = c.Close()
	<-c.Done()
	next, gen, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if next == c || gen != 2 {
		t.Fatalf("reader was not replaced: %d", gen)
	}
	h.b.lost(1, errors.New("late watcher"))
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	if !running {
		t.Fatal("read-only connection failure settled ACP turn")
	}
}

func TestRemarksHistoryMetadataRace(t *testing.T) {
	h, _ := acpHarness(t)
	createACP(t, h)
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
	t.Cleanup(b.closeClient)
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
			t.Fatal("reader not reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestACPRejectsUnexpectedNativeRequests(t *testing.T) {
	h, _ := acpHarness(t)
	createACP(t, h)
	if _, _, err := h.b.conn(t.Context()); err != nil {
		t.Fatal(err)
	}
	response, err := h.fake().Request("item/commandExecution/requestApproval", json.RawMessage(`{"threadId":"thr"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := <-response; got.Error == nil || got.Error.Code != -32601 {
		t.Fatalf("native reader accepted an execution request: %+v", got)
	}
	if code := h.call("POST", "/sessions/thr/send", platforms.SendMessageRequest{Message: "hi"}, nil); code != 204 {
		t.Fatal(code)
	}
	h.next("session.idle", nil)
}
