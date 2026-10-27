package codexprovider

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// Cancel exactly at the transport's pre-write context check, after the busy
// admission has been persisted. No sleeps or spool timing assumptions.
type cancelAtWrite struct {
	context.Context
	cancel   context.CancelFunc
	admitted func() bool
}

func (c cancelAtWrite) Err() error {
	if c.admitted() {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCancelledBeforeDispatchReleasesAdmission(t *testing.T) {
	h := newHarness(t, scripted)
	c, gen, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.b.load(t.Context(), c, gen, "thr"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := cancelAtWrite{Context: ctx, cancel: cancel, admitted: func() bool {
		h.b.mu.Lock()
		defer h.b.mu.Unlock()
		return h.b.sessions["thr"].running
	}}
	if err := h.b.send(request, "thr", platforms.SendMessageRequest{Message: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("send = %v", err)
	}
	if slices.ContainsFunc(h.fake().Calls(), func(c codextest.Call) bool { return c.Method == codexappserver.MethodTurnStart }) {
		t.Fatal("cancelled request reached Codex")
	}
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	if running {
		t.Fatal("definitely-unsent turn stranded admission")
	}
	h.next("session.idle", nil)
	// Reuse the same session and healthy shared connection, then drain it.
	h.start("thr")
	if h.b.clientFor(gen) != c {
		t.Fatal("unsent cancellation replaced the shared connection")
	}
	notify(t, h.fake(), codexappserver.NotifyTurnCompleted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "completed"}})
	h.next("session.idle", nil)
	var drain providerprotocol.DrainState
	if code := h.call(http.MethodPost, "/drain", nil, &drain); code != http.StatusOK || !drain.Idle {
		t.Fatalf("drain = %d %+v", code, drain)
	}
}
