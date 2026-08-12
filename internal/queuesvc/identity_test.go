package queuesvc

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Session identity is (platform, sessionID). Two machines can hand out the
// same bare session id — a local `opencode/s1` and a remote
// `r-A:opencode/s1` — so every piece of in-memory queue coordination has to
// be keyed by the pair. These tests pin that: one platform's queue must
// never observe, drain, or suppress another's.

const (
	localPlatform  = "opencode"
	remotePlatform = "r-A:opencode"
)

// A drain on one platform's session must not disarm the other platform's
// enqueue fast path. The guard used to be keyed by bare session id, so
// sending for opencode/s1 silently held back the very next message for
// r-A:opencode/s1 (same id, different machine).
func TestDrainGuardIsPerPlatform(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)

	// Idle enqueue on the local platform drains immediately and arms the
	// local drain guard.
	if err := svc.Enqueue(context.Background(), localPlatform, false,
		platforms.SendMessageRequest{SessionID: "s1", Message: "local one"}); err != nil {
		t.Fatalf("Enqueue local: %v", err)
	}
	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("sent = %v, want [local one]", got)
	}

	// The remote session with the SAME id is untouched: its own idle
	// enqueue must still take the fast path.
	if err := svc.Enqueue(context.Background(), remotePlatform, false,
		platforms.SendMessageRequest{SessionID: "s1", Message: "remote one"}); err != nil {
		t.Fatalf("Enqueue remote: %v", err)
	}
	if got := sender.messages(); len(got) != 2 || got[1] != "remote one" {
		t.Fatalf("sent = %v, want [local one remote one]; the remote session shares only the bare id", got)
	}
}

// A session.idle edge on one platform must drain only that platform's
// queue, and must not clear the other platform's drain guard.
func TestFlushDrainsOnlyItsOwnPlatform(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	svc := New(store, sender, statusStub{running: true, ok: true}, nil)

	for _, p := range []string{localPlatform, remotePlatform} {
		if err := svc.Enqueue(context.Background(), p, true,
			platforms.SendMessageRequest{SessionID: "s1", Message: p + " held"}); err != nil {
			t.Fatalf("Enqueue %s: %v", p, err)
		}
	}

	svc.Flush(context.Background(), localPlatform, "s1")
	if got := sender.messages(); len(got) != 1 || got[0] != localPlatform+" held" {
		t.Fatalf("sent = %v, want only the local platform's message", got)
	}
	if q, _ := svc.List(remotePlatform, "s1"); len(q) != 1 {
		t.Fatalf("remote queue = %v, want its held message intact", q)
	}

	svc.Flush(context.Background(), remotePlatform, "s1")
	if got := sender.messages(); len(got) != 2 || got[1] != remotePlatform+" held" {
		t.Fatalf("sent = %v, want both platforms drained once each", got)
	}
}

// Removing a message from one platform's queue leaves the other's alone,
// and the resulting notification names the platform that actually changed.
func TestRemoveAndNotifyArePerPlatform(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	var notified []sessionKey
	svc := New(store, sender, statusStub{running: true, ok: true},
		func(platform, sessionID string) {
			notified = append(notified, sessionKey{Platform: platform, SessionID: sessionID})
		})

	for _, p := range []string{localPlatform, remotePlatform} {
		if err := svc.Enqueue(context.Background(), p, true,
			platforms.SendMessageRequest{SessionID: "s1", Message: p + " held"}); err != nil {
			t.Fatalf("Enqueue %s: %v", p, err)
		}
	}
	if len(notified) != 2 || notified[0].Platform != localPlatform || notified[1].Platform != remotePlatform {
		t.Fatalf("notified = %v, want one notification per platform", notified)
	}

	remoteQueue, _ := svc.List(remotePlatform, "s1")
	if len(remoteQueue) != 1 {
		t.Fatalf("remote queue = %v, want one message", remoteQueue)
	}
	notified = nil
	removed, err := svc.Remove("s1", remoteQueue[0].ID)
	if err != nil || !removed {
		t.Fatalf("Remove: removed=%v err=%v", removed, err)
	}
	if len(notified) != 1 || notified[0].Platform != remotePlatform {
		t.Fatalf("notified = %v, want the remote platform only", notified)
	}
	if q, _ := svc.List(localPlatform, "s1"); len(q) != 1 {
		t.Fatalf("local queue = %v, want it untouched by the remote removal", q)
	}
}
