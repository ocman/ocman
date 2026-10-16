package claudeprovider

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func externalHarness(t *testing.T) *liveHarness {
	t.Helper()
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.NoSync = true })
	if err := h.live.idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestContinueExternalSession(t *testing.T) {
	h := externalHarness(t)
	// Keep the real transcript, but point its working directory at a disposable checkout.
	if _, err := h.live.idx.db.Exec("UPDATE session SET directory = ? WHERE id = ?", h.dir, mainID); err != nil {
		t.Fatal(err)
	}
	detail, err := h.adapter.Session(t.Context(), mainID, 10, 0)
	if err != nil || !detail.Session.LiveConnection {
		t.Fatalf("external session composer must be available: %+v %v", detail, err)
	}
	if _, owned := h.live.ownedSession(mainID); owned {
		t.Fatal("reading a session must not claim it")
	}
	if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: "SessionStart", SessionID: mainID, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "external session start observed", func() bool { _, _, n := h.events.get(mainID); return n == 1 })
	if detail, err := h.adapter.Session(t.Context(), mainID, 10, 0); err != nil || len(detail.Warnings) != 1 || !detail.Session.LiveConnection {
		t.Fatalf("owner warning must not lock the composer: %+v %v", detail, err)
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: mainID, Message: "continue"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "external turn completed", func() bool { _, _, n := h.events.get(mainID); return n == 2 })
	args := h.launchArgs(t)
	i := slices.Index(args, "--resume")
	if i < 0 || args[i+1] != mainID || slices.Contains(args, "--session-id") {
		t.Fatalf("must resume the original history: %v", args)
	}
	o, ok := h.live.ownedSession(mainID)
	if !ok || !o.Started || o.Directory != h.dir || o.Config != h.live.opts.ConfigDir {
		t.Fatalf("adopted ownership = %+v, %v", o, ok)
	}
	if detail, err := h.adapter.Session(t.Context(), mainID, 10, 0); err != nil || len(detail.Warnings) != 1 {
		t.Fatalf("adoption erased the external-owner warning: %+v %v", detail, err)
	}
	if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: "SessionEnd", SessionID: mainID, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if detail, err := h.adapter.Session(t.Context(), mainID, 10, 0); err != nil || len(detail.Warnings) != 0 {
		t.Fatalf("external exit left a warning: %+v %v", detail, err)
	}
}

func TestExternalSubagentRemainsReadOnly(t *testing.T) {
	h := externalHarness(t)
	detail, err := h.adapter.Session(t.Context(), childID, 10, 0)
	if err != nil || detail.Session.LiveConnection {
		t.Fatalf("subagent must not be resumed as a root: %+v %v", detail, err)
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: childID, Message: "no"}); err == nil {
		t.Fatal("subagent resumed")
	}
}

func TestExternalExitDoesNotSettleUnfinishedTurn(t *testing.T) {
	h := externalHarness(t)
	for _, event := range []string{"PreToolUse", "SessionEnd"} {
		if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: event, SessionID: otherID}); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := h.adapter.Session(t.Context(), otherID, 10, 0)
	if err != nil || detail.Session.Status != db.StatusInterrupted {
		t.Fatalf("process exit falsely settled an unfinished turn: %+v %v", detail, err)
	}
}

func TestExternalClaimPersistenceFailure(t *testing.T) {
	h := externalHarness(t)
	if err := os.MkdirAll(h.live.opts.StatePath+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: mainID, Message: "continue"})
	if err == nil || strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected ownership persistence failure, got %v", err)
	}
	if h.procs() != 0 {
		t.Fatal("started Claude without durable ownership")
	}
	if _, ok := h.live.ownedSession(mainID); ok {
		t.Fatal("failed adoption left an in-memory owner")
	}
}
