package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// postQueuedMessage holds a message for (platform, sessionID) through the
// real HTTP handler.
func postQueuedMessage(t *testing.T, srv *Server, platform, sessionID, text string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/session/"+sessionID+"/message?platform="+platform,
		strings.NewReader(`{"message":"`+text+`","queue":true}`))
	rr := httptest.NewRecorder()
	srv.handleSessionMessage(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("post %s/%s: status = %d; body=%s", platform, sessionID, rr.Code, rr.Body)
	}
}

// Two machines can hand out the same bare session id. Everything the queue
// does on a session.idle edge — the drain and the queue.updated broadcast —
// must therefore be scoped to (platform, sessionID). Before the fix the
// idle edge carried no platform at all, so an idle edge from the local
// instance drained (and broadcast over) a remote session's queue.
func TestQueueIdentity_IdleEdgeAndBroadcastAreHostScoped(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	sent := map[string][]string{}
	for _, platformID := range []string{"fake", "r-A:fake"} {
		id := platformID
		reg.Register(&fakePlatform{
			id:       id,
			sessions: []db.Session{mkSession(id, "s1", "t", 1)},
			sendMessageFn: func(req platforms.SendMessageRequest) error {
				mu.Lock()
				sent[id] = append(sent[id], req.Message)
				mu.Unlock()
				return nil
			},
			// Both sessions stay mid-turn: only an idle edge may drain.
			sessionDetailFn: func(sessionID string) (*platforms.SessionDetail, error) {
				return &platforms.SessionDetail{Session: &db.Session{ID: sessionID, Status: db.StatusBusy}}, nil
			},
		})
	}

	postQueuedMessage(t, srv, "fake", "s1", "local held")

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	// The remote session's own enqueue broadcasts only its own queue — not
	// the local same-id session's row as well.
	postQueuedMessage(t, srv, "r-A:fake", "s1", "remote held")
	if got := drainQueueUpdated(t, sub.ch); len(got) != 1 || got[0].Text != "remote held" {
		t.Fatalf("queue.updated = %+v, want just the remote session's own queue", got)
	}

	// An idle edge from the local instance drains the local queue only.
	srv.onSessionIdle("fake", "s1")
	waitForSend(t, &mu, sent, "fake", 1)

	mu.Lock()
	defer mu.Unlock()
	if len(sent["fake"]) != 1 || sent["fake"][0] != "local held" {
		t.Fatalf("local sends = %v, want [local held]", sent["fake"])
	}
	if len(sent["r-A:fake"]) != 0 {
		t.Fatalf("remote sends = %v, want none: the idle edge was not its edge", sent["r-A:fake"])
	}
	if msgs, err := srv.stateDB.ListQueuedMessages("r-A:fake", "s1"); err != nil || len(msgs) != 1 {
		t.Fatalf("remote queue = %+v (err %v), want its held message intact", msgs, err)
	}
}

func waitForSend(t *testing.T, mu *sync.Mutex, sent map[string][]string, platform string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(sent[platform])
		mu.Unlock()
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d send(s) on %s", want, platform)
		}
		time.Sleep(time.Millisecond)
	}
}
