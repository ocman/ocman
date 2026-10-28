package remote

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// An unknown method must look like a missing RPC on an older owner, so the
// hub's Unimplemented fallbacks keep working.
func TestCallUnknownMethodIsUnimplemented(t *testing.T) {
	conn := &RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", NewServer(platforms.NewRegistry(), localStubHost{}, "remote", "test")))}
	_, err := conn.call(t.Context(), &pb.CallReq{Method: "NoSuchMethod"}, nil)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("unknown method = %v, want Unimplemented", err)
	}
	if !errors.Is(remotePlatformError(err), platforms.ErrUnsupported) {
		t.Fatalf("unknown method did not map to ErrUnsupported: %v", err)
	}
}

func TestCallRejectsUnmarshalableArgs(t *testing.T) {
	conn := &RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", NewServer(platforms.NewRegistry(), localStubHost{}, "remote", "test")))}
	if _, err := conn.call(t.Context(), &pb.CallReq{Method: "GitBranches"}, make(chan int)); err == nil {
		t.Fatal("unmarshalable args were sent")
	}
}
