package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/startup"
)

type startupFailureModule struct{}

func (startupFailureModule) Name() string                { return "failing-test-module" }
func (startupFailureModule) Routes(module.Router)        {}
func (startupFailureModule) Start(context.Context) error { return errors.New("module start failed") }

type startupModulePlatform struct {
	*fakePlatform
	mod module.Module
}

func (p *startupModulePlatform) SidebarModule() module.Module { return p.mod }

type startupBodyModule struct{ entered chan struct{} }

func (*startupBodyModule) Name() string { return "unfinished-body" }
func (m *startupBodyModule) Routes(r module.Router) {
	r.Handle("POST /api/test-slow-body", func(_ http.ResponseWriter, r *http.Request) {
		close(m.entered)
		_, _ = io.Copy(io.Discard, r.Body)
	})
}

func TestGatewayRetainsFailurePageAfterActualModuleStartFailure(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	s := testServer(t)
	s.registry.Register(&startupModulePlatform{fakePlatform: &fakePlatform{}, mod: startupFailureModule{}})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- startup.Run(ctx, ln, fstest.MapFS{"index.html": {Data: []byte("shell")}}, func(ctx context.Context) error { return s.StartOnListener(ctx, ln) })
	}()
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(5 * time.Second); ; {
		select {
		case err := <-done:
			t.Fatalf("module failure closed gateway: %v", err)
		default:
		}
		resp, err := client.Get("http://" + ln.Addr().String() + "/api/startup")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(body), `"failed"`) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("failure UI unavailable", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "module start failed") {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway failed to stop")
	}
}

func TestGatewayCancellationBoundsUnfinishedRequestBody(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 20 * time.Millisecond
	defer func() { shutdownTimeout = old }()
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	// Repeat to exercise both ready select branches at cancellation.
	for range 4 {
		s := testServer(t)
		bodyModule := &startupBodyModule{entered: make(chan struct{})}
		s.registry.Register(&startupModulePlatform{fakePlatform: &fakePlatform{}, mod: bodyModule})
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- startup.Run(ctx, ln, fstest.MapFS{"index.html": {Data: []byte("shell")}}, func(ctx context.Context) error { return s.StartOnListener(ctx, ln) })
		}()
		client := &http.Client{Timeout: time.Second}
		for deadline := time.Now().Add(5 * time.Second); ; {
			resp, err := client.Get("http://" + ln.Addr().String() + "/api/system/stats")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusServiceUnavailable {
					break
				}
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("startup timed out")
			}
			time.Sleep(time.Millisecond)
		}
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(conn, "POST /api/test-slow-body HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{")
		select {
		case <-bodyModule.entered:
		case <-time.After(5 * time.Second):
			conn.Close()
			cancel()
			<-done
			t.Fatal("unfinished body was never admitted")
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				conn.Close()
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			conn.Close()
			<-done
			t.Fatal("unfinished body bypassed bounded gateway shutdown")
		}
		conn.Close()
	}
}

func TestClosedServeChannelStillShutsDownAdmittedBody(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = time.Millisecond
	defer func() { shutdownTimeout = old }()
	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		entered, finished := make(chan struct{}), make(chan struct{})
		srv := newHTTPServer(ln.Addr().String(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(entered)
			_, _ = io.Copy(io.Discard, r.Body)
			close(finished)
		}))
		go func() { _ = srv.Serve(ln) }()
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n{")
		<-entered
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		errCh := make(chan error)
		close(errCh)
		err = awaitHTTP(ctx, srv, errCh)
		select {
		case <-finished:
		case <-time.After(100 * time.Millisecond):
			srv.Close()
			conn.Close()
			<-finished
			t.Fatal("closed serve channel bypassed bounded HTTP shutdown")
		}
		srv.Close()
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
