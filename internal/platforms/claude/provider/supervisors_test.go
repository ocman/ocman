package claudeprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// Two project supervisors share the machine-wide cap: with every slot held
// by another supervisor's idle child, a turn waits until that child is
// evicted, and never runs more children than the cap.
func TestMachineWideCapAcrossSupervisors(t *testing.T) {
	slots := t.TempDir()
	shared := func(o *Options) { o.SlotDir, o.MaxProcs = slots, 1 }
	a := startLive(t, claudetest.Fixture("plain.jsonl"), shared)
	b := startLive(t, claudetest.Fixture("plain.jsonl"), shared)
	first := a.create(t)
	a.turn(t, first, 1)
	if a.procs() != 1 {
		t.Fatal("finished child should idle, holding its slot")
	}
	second := b.create(t)
	if err := b.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: second, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "waiting supervisor ran its turn", func() bool { _, _, n := b.events.get(second); return n == 1 })
	if a.procs() != 0 {
		t.Fatal("idle child of the other supervisor kept its slot")
	}
	if waiters, _ := filepath.Glob(filepath.Join(slots, "wait-*")); len(waiters) != 0 {
		t.Fatalf("waiter left behind: %v", waiters)
	}
	// A crashed waiter's file is stale, not a reason to evict.
	stale := filepath.Join(slots, "wait-stale")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if othersWaiting(slots) {
		t.Fatal("stale waiter counted")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale waiter not removed")
	}
}

// Every supervisor shares claude-sessions.json: concurrent creates in two
// supervisors are all kept and each sees the other's sessions.
func TestOwnershipSharedAcrossSupervisors(t *testing.T) {
	state := filepath.Join(t.TempDir(), "claude-sessions.json")
	shared := func(o *Options) { o.StatePath = state }
	a := serveLive(t, claudetest.Fixture("plain.jsonl"), shared)
	b := serveLive(t, claudetest.Fixture("plain.jsonl"), shared)
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := range 20 {
		h := a
		if i%2 == 1 {
			h = b
		}
		wg.Go(func() { ids <- h.create(t) })
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		if _, ok := a.live.ownedSession(id); !ok {
			t.Fatalf("supervisor a lost %s", id)
		}
		if _, ok := b.live.ownedSession(id); !ok {
			t.Fatalf("supervisor b lost %s", id)
		}
		if dir, ok := OwnedDirectory(state, id); !ok || dir != a.dir && dir != b.dir {
			t.Fatalf("OwnedDirectory(%s) = %q %v", id, dir, ok)
		}
	}
	if _, ok := OwnedDirectory(state, "terminal"); ok {
		t.Fatal("unowned session reported owned")
	}
	if _, ok := OwnedDirectory(filepath.Join(t.TempDir(), "missing.json"), "x"); ok {
		t.Fatal("missing state file")
	}
}

// /drain reports idle only when no session has an open turn: not while a
// prompt waits, not while the reply streams, only after the result.
func TestDrainWaitsForTrulyIdleSessions(t *testing.T) {
	hold := filepath.Join(t.TempDir(), "release")
	t.Setenv("FAKE_CLAUDE_HOLD", hold)
	h, got := promptHarness(t, claudetest.Fixture("permission.jsonl"), nil)
	socket := filepath.Join(h.dir, "s")
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	drain := func() bool {
		t.Helper()
		resp, err := client.Post("http://provider/drain", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var state providerprotocol.DrainState
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			t.Fatal(err)
		}
		return state.Idle
	}
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "permission asked", func() bool { return len(got.props("permission.asked")) == 1 })
	if drain() {
		t.Fatal("drained with a pending permission")
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "more"}); err == nil {
		t.Fatal("draining supervisor admitted a new turn")
	}
	asked := got.props("permission.asked")[0]
	if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: fmt.Sprint(asked["id"]), Reply: "once"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reply streamed", func() bool { return len(got.props("permission.replied")) == 1 })
	if drain() {
		t.Fatal("drained while the turn streams")
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if !drain() {
		t.Fatal("finished turn did not drain")
	}
}
