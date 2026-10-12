package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factorymod"
)

// lifecycleFactory records what was reachable when Factory started and
// stopped. Its sessions call ocman's MCP tools, so dispatch must start with
// the MCP listener up, and it must stop only once no request can reach it.
type lifecycleFactory struct {
	factorymod.Service
	mu                        sync.Mutex
	srv                       *Server
	main                      string
	startErr                  error
	attention, mcpUpAtStart   bool
	mcpDown, httpDown, closed bool
}

func (f *lifecycleFactory) OnAttention(func(context.Context, string, []string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attention = true
}

func (f *lifecycleFactory) Status(context.Context) factory.Status { return factory.Status{} }

func (f *lifecycleFactory) Start(context.Context) error {
	up := dials(f.srv.mcpAddr)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpUpAtStart = up
	return f.startErr
}

func (f *lifecycleFactory) Close() {
	mcpDown, httpDown := !dials(f.srv.mcpAddr), !dials(f.main)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpDown, f.httpDown, f.closed = mcpDown, httpDown, true
}

func dials(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
	}
	return err == nil
}

func startFactoryLifecycle(t *testing.T, startErr error) (*lifecycleFactory, context.CancelFunc, <-chan error) {
	t.Helper()
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	srv.mcpAddr = "127.0.0.1:0"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &lifecycleFactory{srv: srv, main: ln.Addr().String(), startErr: startErr}
	useFactory(srv, f)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.StartOnListener(ctx, ln) }()
	return f, cancel, done
}

func TestFactoryLaunchesWithMCPAndClosesAfterListeners(t *testing.T) {
	f, cancel, done := startFactoryLifecycle(t, nil)
	defer cancel()
	for {
		resp, err := http.Get("http://" + f.main + "/api/factory/status")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case err := <-done:
			t.Fatalf("server exited during startup: %v", err)
		}
	}
	f.mu.Lock()
	attention, mcpUp, closed := f.attention, f.mcpUpAtStart, f.closed
	f.mu.Unlock()
	if !attention || !mcpUp || closed {
		t.Fatalf("started: attention %v, MCP up %v, closed %v", attention, mcpUp, closed)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !f.closed || !f.mcpDown || !f.httpDown {
		t.Fatalf("closed %v with MCP down %v, HTTP down %v", f.closed, f.mcpDown, f.httpDown)
	}
}

func TestFactoryLaunchFailureAbortsStartup(t *testing.T) {
	f, cancel, done := startFactoryLifecycle(t, errors.New("boom"))
	defer cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "starting Factory: boom") {
		t.Fatalf("startup error = %v", err)
	}
	if f.closed || dials(f.srv.mcpAddr) {
		t.Fatalf("failed launch left closed=%v MCP listening=%v", f.closed, dials(f.srv.mcpAddr))
	}
}
