package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestServerShutdownWaitsForStartedWorkers(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	original := autoArchiveTickFn
	autoArchiveTickFn = func(context.Context, *Server) { close(entered); <-release; close(exited) }
	defer func() { autoArchiveTickFn = original }()
	defer func() { close(release); <-exited }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.StartOnListener(ctx, ln) }()
	<-entered
	// Wait for startup to complete, so only the blocked worker can delay shutdown.
	for {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/routines")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before its worker exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// The deferred release lets the worker finish before the fixture is removed.
	t.Cleanup(func() {
		if err := <-done; err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})
}

type blockedArchivePlatform struct {
	fakePlatform
	entered chan context.Context
	release chan struct{}
}

func (p *blockedArchivePlatform) SessionsInactiveBefore(ctx context.Context, _ int64) ([]db.SessionArchiveCandidate, error) {
	p.entered <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled
	}
}

func TestServerShutdownCancelsBlockedArchiveRPC(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	remote := &blockedArchivePlatform{entered: make(chan context.Context, 1), release: make(chan struct{})}
	srv.registry = platforms.NewRegistry()
	srv.registry.Register(remote)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	t.Cleanup(func() { cancel(); close(remote.release); <-done })
	go func() { err := srv.StartOnListener(ctx, ln); done <- err; close(done) }()
	rpcCtx := <-remote.entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung on the blocked archive RPC")
	}
	if deadline, ok := rpcCtx.Deadline(); !ok || time.Until(deadline) > time.Minute {
		t.Fatal("archive RPC has no bounded deadline")
	}
}

func TestServerShutdownCancelsBlockedProjectRefresh(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	entered, release := make(chan context.Context, 1), make(chan struct{})
	srv.projects.fetch = func() ([]db.ProjectStats, error) { return nil, nil }
	srv.projects.enrich = func(ctx context.Context, _ []db.ProjectStats) error {
		entered <- ctx
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return context.Canceled
		}
	}
	if err := srv.activity.Update(clientActivityLease{ClientID: "client", Visible: true, Scopes: []string{"projects"}, TTLMS: 45_000}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	t.Cleanup(func() { cancel(); close(release); <-done })
	go func() { err := srv.StartOnListener(ctx, ln); done <- err; close(done) }()
	refreshCtx := <-entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung on blocked project refresh")
	}
	if deadline, ok := refreshCtx.Deadline(); !ok || time.Until(deadline) > time.Minute {
		t.Fatal("project refresh has no bounded deadline")
	}
}
