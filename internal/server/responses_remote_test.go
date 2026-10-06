package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCanceledRPCResponses(t *testing.T) {
	hub, registry := newSessionsTestServer(t)
	host := local.New(local.Deps{})
	hub.hostRouter = hostsvc.NewRouter(host)
	ownerRegistry := platforms.NewRegistry()
	ownerRegistry.Register(&fakePlatform{id: "opencode"})
	listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, remote.NewServer(ownerRegistry, host, "machine", "test"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	manager := remote.NewManager(registry, hub.hostRouter, hub.stateDB, "opencode")
	t.Cleanup(manager.Stop)
	if _, err := manager.Add(t.Context(), "grpc://"+listener.Addr(), "token", "Owner"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, connected := hub.hostRouter.LookupRemote("machine")
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote did not connect")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	owner, _ := hub.hostRouter.LookupRemote("machine")
	adapter, ok := registry.Get("r-machine:opencode")
	if !ok {
		t.Fatal("remote platform not registered")
	}
	for _, name := range []string{"host beads", "host git", "platform"} {
		t.Run(name, func(t *testing.T) {
			var rpcErr error
			switch name {
			case "host beads":
				_, rpcErr = owner.BeadsStatus(ctx, "/remote/repo")
			case "host git":
				_, rpcErr = owner.GitDiff(ctx, "/remote/repo", hostsvc.GitDiffOptions{})
			default:
				_, rpcErr = adapter.Session(ctx, "s1", 0, 0)
			}
			if status.Code(rpcErr) != codes.Canceled {
				t.Fatalf("RPC error = %v, want codes.Canceled", rpcErr)
			}
			w := httptest.NewRecorder()
			if name == "host beads" {
				hub.handleProjectBeadsStatus(w, httptest.NewRequest(http.MethodGet, "/api/project/beads-status?dir=/remote/repo&remoteId=machine", nil).WithContext(ctx))
			} else {
				writePlatformError(w, "remote read", rpcErr)
			}
			if w.Code != 499 {
				t.Fatalf("status = %d, want 499; body=%s", w.Code, w.Body)
			}
		})
	}
}
