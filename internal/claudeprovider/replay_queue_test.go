package claudeprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

type heldSends struct {
	mu   sync.Mutex
	sent []string
}

func (s *heldSends) SendNow(_ context.Context, _ string, req platforms.SendMessageRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, req.Message)
	return nil
}

func (s *heldSends) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

type running bool

func (r running) TurnRunning(context.Context, string, string) (bool, bool) { return bool(r), true }

// Turn A's late resolution keeps A's ask in the spool after A's idle edge
// was acknowledged. Turn B then runs while the user holds C. A dashboard
// restart (fresh adapter replaying the spool from the start, wired to the
// follow-up queue as the server wires it) must not treat A's acknowledged
// idle edge as B's end: C stays held until B is actually idle.
func TestRetainedIdleDoesNotDrainQueueIntoNextTurn(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h, id, _ := lateResolution(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"B"}}
`, gate))
	h.send(t, id, "B")
	waitFor(t, "B running", func() bool {
		h.live.mu.Lock()
		defer h.live.mu.Unlock()
		p := h.live.procs[id]
		return p != nil && p.busy && len(h.live.waits[id]) == 0
	})

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sends := &heldSends{}
	q := queuesvc.New(st, sends, running(true), nil)
	platform := string(h.adapter.ID())
	if err := q.Enqueue(t.Context(), platform, true, platforms.SendMessageRequest{SessionID: id, Message: "C"}); err != nil {
		t.Fatal(err)
	}

	h.adapter.Close() // the dashboard restarts; B and its supervisor survive
	restarted, err := provider.New(t.Context(), filepath.Join(h.dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	restarted.SetSessionEventSink(platforms.SessionEventSink{OnSessionIdle: func(p, sid string) { q.Flush(context.Background(), p, sid) }})
	go func() { _ = restarted.WatchEvents(t.Context()) }()
	waitFor(t, "replayed to B", func() bool {
		s := restarted.LiveStatus(id)
		return s != nil && s.Status == "busy"
	})
	time.Sleep(200 * time.Millisecond)
	if n := sends.count(); n != 0 {
		t.Fatal("a replayed, acknowledged idle edge drained C into running turn B")
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "C after B's idle", func() bool { return sends.count() == 1 })
}
