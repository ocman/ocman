package claudecode

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor/providertest"
)

// sseBrowser streams one event for session then holds the stream open.
func sseBrowser(t *testing.T, session string) *provider.Adapter {
	t.Helper()
	a, err := provider.New(t.Context(), sseSocket(t, session, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// sseSocket serves /hello and an /events stream that stays open; each
// stream is reported on opened.
func sseSocket(t *testing.T, session string, opened chan<- struct{}) string {
	t.Helper()
	socket := filepath.Join(providertest.Directory(t), "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hello" {
			_ = json.NewEncoder(w).Encode(providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "claudecode", CLIVersion: "test",
				Capabilities: providerprotocol.Capabilities{Capabilities: platforms.Capabilities{Events: true}}})
			return
		}
		if r.URL.Path == "/sessions" {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "id: 1\ndata: {\"type\":\"message.updated\",\"properties\":{\"sessionID\":%q,\"browse\":true}}\n\n", session)
		w.(http.Flusher).Flush()
		if opened != nil {
			opened <- struct{}{}
		}
		<-r.Context().Done()
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket
}

func drainingAdapter(t *testing.T) *provider.Adapter {
	t.Helper()
	_, socket := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	a, err := provider.NewDraining(t.Context(), socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// blockingWriter holds the browsing stream's write until released.
type blockingWriter struct {
	active  atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), `"browse":true`) {
		b.active.Add(1)
		defer b.active.Add(-1)
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return len(p), nil
}

// The browsing goroutine writes to the response; the handler may not
// return while it still can.
func TestProxyDrainingJoinsBrowsingStream(t *testing.T) {
	const session = "s1"
	p := &claudePlatform{Adapter: sseBrowser(t, session)}
	d := &drained{watched: watched{adapter: drainingAdapter(t)}, done: make(chan struct{}), subs: map[chan providerprotocol.Event]string{}}
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	result := make(chan int32, 1)
	go func() {
		_ = p.proxyDraining(t.Context(), session, d, w, nil)
		result <- w.active.Load()
	}()
	<-w.entered
	close(d.done)
	time.AfterFunc(100*time.Millisecond, func() { close(w.release) })
	if active := <-result; active != 0 {
		t.Fatal("proxyDraining returned while the browsing stream was still writing")
	}
}

func TestProxyDrainingReturnsPromptListError(t *testing.T) {
	const session = "s1"
	p := &claudePlatform{Adapter: sseBrowser(t, session)}
	plain, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	d := &drained{watched: watched{adapter: plain}, done: make(chan struct{}), subs: map[chan providerprotocol.Event]string{}}
	close(d.done)
	if err := p.proxyDraining(t.Context(), session, d, &strings.Builder{}, nil); err == nil {
		t.Fatal("an unreadable prompt list was ignored")
	}
}

// A subscriber that falls behind loses its stream rather than a prompt: the
// client reconnects and lists what is pending.
func TestDrainedOverflowClosesSubscriber(t *testing.T) {
	d := &drained{subs: map[chan providerprotocol.Event]string{}}
	ch, unsubscribe := d.subscribe("s1")
	defer unsubscribe()
	props, _ := json.Marshal(map[string]string{"sessionID": "s1"})
	for range cap(ch) + 1 {
		d.broadcast(providerprotocol.Event{Type: "permission.asked", Properties: props})
	}
	for range cap(ch) {
		<-ch
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected extra event")
		}
	default:
		t.Fatal("an overflowing subscriber silently dropped an event")
	}
}

// Closing the projects joins every event stream, and nothing attaches after.
func TestClaudeProjectsCloseJoinsEventStreams(t *testing.T) {
	opened := make(chan struct{}, 4)
	socket := sseSocket(t, "s1", opened)
	c := &claudeProjects{roots: map[string]*claudeProject{}, draining: map[string]*drained{}, attached: make(chan struct{}), watch: t.Context()}
	if _, err := c.attach(t.Context(), "/repo", "unix://"+socket); err != nil {
		t.Fatal(err)
	}
	<-opened
	p := c.roots["/repo"]
	c.close()
	select {
	case <-p.watching:
	default:
		t.Fatal("close returned while an event stream still ran")
	}
	if _, err := c.attach(t.Context(), "/other", "unix://"+socket); err == nil {
		t.Fatal("attached after close")
	}
}
