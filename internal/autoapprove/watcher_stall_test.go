package autoapprove

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

const heartbeatEvent = `data: {"directory":"global","payload":{"type":"server.heartbeat","properties":{}}}` + "\n\n"

// stallSlack absorbs scheduling noise on a loaded -race runner; the bound
// under test is idleTimeout + reconnectDelay.
const stallSlack = 2 * time.Second

func writeFlush(w http.ResponseWriter, s string) {
	_, _ = w.Write([]byte(s))
	w.(http.Flusher).Flush()
}

// A stream that stops producing bytes while its port stays listed must be
// dropped after the idle bound, so the reconnect reseeds status and
// reconciles the prompts the silent stream never delivered.
func TestAutoApproveWatcherReconnectsSilentStreamOnListedPort(t *testing.T) {
	var conns, statusReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/global/event":
			n := conns.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 1 {
				writeFlush(w, heartbeatEvent) // then go silent, connection open
			} else {
				writeFlush(w, `data: {"type":"session.status","properties":{"sessionID":"ses-1","status":{"type":"busy"}}}`+"\n\n")
			}
			<-r.Context().Done()
		case "/session/status":
			statusReads.Add(1)
			_, _ = w.Write([]byte(`{}`))
		case "/permission":
			if conns.Load() >= 2 {
				_, _ = w.Write([]byte(`[{"id":"perm-missed","sessionID":"ses-1","permission":"Bash","patterns":[],"metadata":{}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	adapter := opencode.New(nil, nil)
	statuses := make(chan db.SessionStatus, 4)
	svc := NewService(Deps{
		OpencodePlatform:       func() platforms.Platform { return adapter },
		BroadcastSessionStatus: func(_ string, s db.SessionStatus) { statuses <- s },
	})
	w := newAutoApproveWatcher(svc)
	w.discoverPorts = func() map[string]string { return map[string]string{"/repo/stall": port} }
	w.rescanInterval = 20 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond
	w.idleTimeout = 150 * time.Millisecond
	permissions := make(chan string, 4)
	w.onPermission = func(_ platforms.ID, _ platforms.Platform, sessionID, permissionID, _ string, _ []string, _ map[string]any) {
		permissions <- sessionID + "/" + permissionID
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	go w.run(ctx)

	deadline := w.idleTimeout + w.reconnectDelay + stallSlack
	select {
	case got := <-permissions:
		if got != "ses-1/perm-missed" {
			t.Fatalf("reconciled permission = %q", got)
		}
	case <-time.After(deadline):
		t.Fatalf("silent stream was not replaced within %v (connections=%d)", deadline, conns.Load())
	}
	select {
	case got := <-statuses:
		if got != db.StatusBusy {
			t.Fatalf("status = %q, want busy", got)
		}
	case <-time.After(stallSlack):
		t.Fatal("updated status from the new connection was not observed")
	}
	if elapsed := time.Since(start); elapsed < w.idleTimeout {
		t.Fatalf("reconnected after %v, before the %v idle bound", elapsed, w.idleTimeout)
	}
	if got := statusReads.Load(); got < 2 {
		t.Fatalf("/session/status reads = %d, want a reseed on reconnect", got)
	}
}

// OpenCode heartbeats every 10s; regular bytes must keep a quiet but
// healthy stream subscribed.
func TestAutoApproveWatcherHeartbeatsKeepStreamAlive(t *testing.T) {
	var conns atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/event" {
			http.NotFound(w, r)
			return
		}
		conns.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			writeFlush(w, heartbeatEvent)
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
		}
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	w := newAutoApproveWatcher(nil)
	w.discoverPorts = func() map[string]string { return map[string]string{"/repo/hb": port} }
	w.rescanInterval = 20 * time.Millisecond
	w.reconnectDelay = 10 * time.Millisecond
	w.idleTimeout = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	testutil.WaitFor(t, waitTimeout, "the watcher to subscribe", func() bool { return conns.Load() == 1 })
	time.Sleep(4 * w.idleTimeout)
	if got := conns.Load(); got != 1 {
		t.Fatalf("connections = %d, want 1: heartbeats must keep the stream alive", got)
	}
}

// An upstream that accepts the connection but never sends headers must not
// hold streamOnce forever either.
func TestStreamOnceBoundsResponseHeaderWait(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	w := newAutoApproveWatcher(nil)
	w.idleTimeout = 100 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- w.streamOnce(context.Background(), port) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("streamOnce returned nil for a header stall")
		}
	case <-time.After(w.idleTimeout + stallSlack):
		t.Fatal("streamOnce still waiting for response headers")
	}
}

func streamOnceGoroutines() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "autoapprove.(*autoApproveWatcher).streamOnce") {
			count++
		}
	}
	return count
}

// Cancelling a healthy stream returns promptly and leaves none of the
// stream's goroutines behind.
func TestStreamOnceCancellationLeavesNoGoroutines(t *testing.T) {
	fake := newFakeOpenCodeEventServer([]string{heartbeatEvent})
	fake.setEvents([]string{heartbeatEvent}, true)
	defer fake.close()

	before := streamOnceGoroutines()
	w := newAutoApproveWatcher(nil)
	w.idleTimeout = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.streamOnce(ctx, fake.port()) }()
	testutil.WaitFor(t, waitTimeout, "the stream to connect", func() bool { return atomic.LoadInt32(&fake.conns) == 1 })
	cancel()
	select {
	case err := <-done:
		// The server counts the connection before the client receives headers.
		// Cancellation may therefore finish either the connect or the read.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled streamOnce = %v, want nil or context.Canceled", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("streamOnce did not return after cancellation")
	}
	testutil.WaitFor(t, waitTimeout, "stream goroutines to exit", func() bool { return streamOnceGoroutines() <= before })
}
