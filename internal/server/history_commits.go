package server

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/statev2"
)

// historyCommitInterval coalesces the commits of a busy turn into one notice.
const historyCommitInterval = 250 * time.Millisecond

// historyCommits fans stored-history commits out to the owner's open session
// streams. Viewers then read what the live stream missed (a gap, a restart
// or content that streamed while nothing was attached).
type historyCommits struct {
	mu        sync.Mutex
	listeners map[string]map[chan int64]struct{}
}

func newHistoryCommits() *historyCommits {
	return &historyCommits{listeners: map[string]map[chan int64]struct{}{}}
}

// observe is the statev2 commit observer; it never blocks the committer.
// Listeners are keyed by session ID alone: statev2 keys carry ocman's
// canonical session ID, unique across source instances, not the native ID
// that several instances may share.
func (h *historyCommits) observe(key statev2.Key, revision int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.listeners[key.SessionID] {
		// Keep only the newest revision for a slow forwarder. The send cannot
		// block: h.mu serialises senders and the forwarder is the only
		// receiver, so after the drain the one-slot buffer is empty.
		select {
		case <-ch:
		default:
		}
		ch <- revision
	}
}

func (h *historyCommits) subscribe(sessionID string) (<-chan int64, func()) {
	ch := make(chan int64, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listeners[sessionID] == nil {
		h.listeners[sessionID] = map[chan int64]struct{}{}
	}
	h.listeners[sessionID][ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.listeners[sessionID], ch)
		if len(h.listeners[sessionID]) == 0 {
			delete(h.listeners, sessionID)
		}
	}
}

// forward writes ocman.history.committed frames for sessionID to w until the
// returned stop runs; stop waits for the last write.
func (h *historyCommits) forward(ctx context.Context, sessionID string, w io.Writer, flush func()) (stop func()) {
	if h == nil || sessionID == "" {
		return func() {}
	}
	commits, unsubscribe := h.subscribe(sessionID)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case revision := <-commits:
				data, _ := json.Marshal(map[string]any{"type": "ocman.history.committed",
					"properties": map[string]any{"sessionID": sessionID, "revision": revision}})
				autoapprove.WriteSSEEvent(w, flush, "ocman.history.committed", data)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(historyCommitInterval):
			}
		}
	}()
	return func() {
		cancel()
		<-done
		unsubscribe()
	}
}
