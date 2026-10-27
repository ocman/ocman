package codexprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// harness runs a supervisor on a private socket against fake app-servers.
// Every connection the backend starts gets a fresh fake, set up by setup.
type harness struct {
	t      *testing.T
	b      *backend
	client *http.Client
	events chan providerprotocol.Event

	mu    sync.Mutex
	fakes []*codextest.Server
	setup func(*codextest.Server)
	fail  error // returned by the next start
	spool string
}

func newHarness(t *testing.T, setup func(*codextest.Server)) *harness {
	t.Helper()
	dir, err := os.MkdirTemp("", "cx-") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{t: t, setup: setup, events: make(chan providerprotocol.Event, 1024), spool: filepath.Join(dir, "spool")}
	start := func(ctx context.Context, handler codexappserver.Handler) (*codexappserver.Client, error) {
		h.mu.Lock()
		if err := h.fail; err != nil {
			h.fail = nil
			h.mu.Unlock()
			return nil, err
		}
		s, pipes := codextest.New(t)
		h.fakes = append(h.fakes, s)
		h.mu.Unlock()
		if h.setup != nil {
			h.setup(s)
		}
		return codexappserver.Attach(ctx, pipes.Reader, pipes.Writer, codexappserver.Options{}, handler)
	}
	sup, b, err := newSupervisor(ctx, Options{Start: start}, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	h.b = b
	socket := filepath.Join(dir, "s")
	served := make(chan struct{})
	go func() { _ = sup.Serve(ctx, socket); close(served) }()
	h.client = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	t.Cleanup(func() { cancel(); <-served; h.client.CloseIdleConnections() })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if resp, err := h.client.Get("http://codex/hello"); err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not start")
		}
	}
	go h.stream(ctx)
	return h
}

// fake returns the most recent fake app-server.
func (h *harness) fake() *codextest.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.fakes) == 0 {
		return nil
	}
	return h.fakes[len(h.fakes)-1]
}

func (h *harness) stream(ctx context.Context) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://codex/events?after=0", nil)
	resp, err := h.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, providerprotocol.MaxEventBytes)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var e providerprotocol.Event
		if json.Unmarshal([]byte(data), &e) == nil {
			h.events <- e
		}
	}
}

// call makes a protocol request and decodes a JSON response into out.
func (h *harness) call(method, path string, body, out any) int {
	h.t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, "http://codex"+path, bytes.NewReader(data))
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			h.t.Fatal(err)
		}
	}
	return resp.StatusCode
}

// next returns the next event of type typ matching ok, failing after a
// timeout. Events skipped on the way are dropped.
func (h *harness) next(typ string, ok func(map[string]any) bool) map[string]any {
	h.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-h.events:
			var props map[string]any
			_ = json.Unmarshal(e.Properties, &props)
			if e.Type == typ && (ok == nil || ok(props)) {
				return props
			}
		case <-timeout:
			h.t.Fatalf("no %s event", typ)
			return nil
		}
	}
}

func info(props map[string]any) map[string]any { return props["info"].(map[string]any) }
func part(props map[string]any) map[string]any { return props["part"].(map[string]any) }
