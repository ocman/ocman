package remote

import (
	"context"
	"errors"
	"github.com/ocman/ocman/internal/platforms"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEnsureRuntimeAndLegacyRPC(t *testing.T) {
	conn := connectedPair(t, &fakePlatform{id: "opencode"})
	host := newRemoteHost(conn)
	result, err := host.EnsureRuntime(t.Context(), "fake", "/repo")
	if err != nil || result.Endpoint != "unix:///tmp/provider.sock" || result.Port() != "" || result.RepoRoot != "/repo" {
		t.Fatalf("provider result = %+v, %v", result, err)
	}
	// An old peer sends the unchanged old method and payload, with no platform.
	response, err := conn.Client().Call(t.Context(), &pb.CallReq{Method: "EnsureProjectOpencode", Payload: []byte(`{"projectDir":"/repo"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var old hostsvc.EnsureRuntimeResult
	if err := unmarshalJSON(response.Payload, &old); err != nil || old.Port() != "1234" {
		t.Fatalf("legacy result = %+v, %v", old, err)
	}
	current, err := host.EnsureRuntime(t.Context(), "opencode", "/repo")
	if err != nil || *current != old {
		t.Fatalf("current = %+v, %v; old = %+v", current, err, old)
	}
}

type oldRuntimeClient struct {
	pb.OcmanClient
	calls int
}

func (c *oldRuntimeClient) Call(_ context.Context, req *pb.CallReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	if req.Method == "EnsureRuntime" {
		return nil, status.Error(codes.Unimplemented, "old remote")
	}
	c.calls++
	return &pb.JsonResp{Payload: []byte(`{"endpoint":"http://127.0.0.1:1234"}`)}, nil
}

func TestEnsureRuntimeFallsBackOnlyForOpenCode(t *testing.T) {
	client := &oldRuntimeClient{}
	host := newRemoteHost(&RemoteConn{client: client})
	if result, err := host.EnsureRuntime(t.Context(), "opencode", "/repo"); err != nil || result.Port() != "1234" || client.calls != 1 {
		t.Fatalf("fallback = %+v, %v, calls %d", result, err, client.calls)
	}
	if _, err := host.EnsureRuntime(t.Context(), "fake", "/repo"); status.Code(err) != codes.Unimplemented || client.calls != 1 {
		t.Fatalf("provider fallback = %v, calls %d", err, client.calls)
	}
	if _, err := newRemoteHost(&RemoteConn{}).EnsureRuntime(t.Context(), "fake", "/repo"); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline = %v", err)
	}
	conn := connectedPair(t, &fakePlatform{id: "opencode"})
	if _, err := conn.Client().Call(t.Context(), &pb.CallReq{Method: "EnsureRuntime", Payload: []byte(`invalid`)}); err == nil {
		t.Fatal("invalid runtime payload accepted")
	}
}

type worktreeRuntimeHost struct {
	localStubHost
	platform string
}

func (h *worktreeRuntimeHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.platform = req.Platform
	return &hostsvc.WorktreeSessionResult{SessionID: "child"}, nil
}

func TestLegacyWorktreeRPCPinsOpenCode(t *testing.T) {
	host := &worktreeRuntimeHost{}
	srv := NewServer(nil, host, "id", "test")
	for _, test := range []struct{ payload, want string }{{`{"ProjectDir":"/repo"}`, "opencode"}, {`{"ProjectDir":"/repo","Platform":"fake"}`, "fake"}} {
		if _, err := srv.CreateWorktreeSession(t.Context(), &pb.CallReq{Payload: []byte(test.payload)}); err != nil {
			t.Fatal(err)
		}
		if host.platform != test.want {
			t.Fatalf("platform = %q, want %q", host.platform, test.want)
		}
	}
}
