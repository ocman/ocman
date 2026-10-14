package server

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func TestServerShutdownWaitsForBackgroundRefresh(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	srv.activity = nil // A visible client's project demand.
	started, release := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return nil, nil
	}
	srv.projects.enrich = func(context.Context, []db.ProjectStats) error { return nil }
	ctx, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); releaseOnce.Do(func() { close(release) }) }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.StartOnListener(ctx, ln) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("project refresh did not start")
	}
	// Ensure startup has completed before testing shutdown, not startup timing.
	for deadline := time.Now().Add(5 * time.Second); ; {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/system/stats")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	returned := false
	select {
	case err := <-done:
		returned = true
		t.Errorf("server returned before the background refresh settled: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if !returned {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown did not finish after releasing the refresh")
		}
	}
}

type healthWorkerHost struct {
	hostsvc.Host
	started, release chan struct{}
}

func (h healthWorkerHost) RunOpenCodeHealthChecks(ctx context.Context) {
	close(h.started)
	<-ctx.Done()
	<-h.release
}

func TestServerStartsAndJoinsRuntimeHealthWorker(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	host := healthWorkerHost{Host: srv.router().Local(), started: make(chan struct{}), release: make(chan struct{})}
	srv.hostRouter = hostsvc.NewRouter(host)
	ctx, cancel := context.WithCancel(t.Context())
	var once sync.Once
	unblock := func() { once.Do(func() { close(host.release) }) }
	defer func() { cancel(); unblock() }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.StartOnListener(ctx, ln) }()
	select {
	case <-host.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime health worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server returned before joining health worker: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish joining health worker")
	}
}
