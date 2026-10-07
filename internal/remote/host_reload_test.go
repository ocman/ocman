package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type reloadOwnerHost struct {
	localStubHost
	calls int
	err   error
}

func (h *reloadOwnerHost) ReloadOpencode(context.Context) error {
	h.calls++
	return h.err
}

func TestReloadOpencodeRPC(t *testing.T) {
	for _, want := range []error{nil, platforms.ErrUnsupported, errors.New("reload failed")} {
		owner := &reloadOwnerHost{err: want}
		conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), owner, "rid", "v"))
		host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
		err := host.ReloadOpencode(t.Context())
		if (err == nil) != (want == nil) || owner.calls != 1 {
			t.Fatalf("error=%v calls=%d, want=%v", err, owner.calls, want)
		}
		if errors.Is(want, platforms.ErrUnsupported) && !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("unsupported error lost across RPC: %v", err)
		}
	}
}

type oldReloadClient struct{ pb.OcmanClient }

func (oldReloadClient) ReloadOpencode(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "unknown method ReloadOpencode")
}

func TestReloadOpencodeOlderRemoteUnsupported(t *testing.T) {
	host := newRemoteHost(&RemoteConn{client: oldReloadClient{}, remoteID: "rid"})
	if err := host.ReloadOpencode(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("error=%v, want unsupported", err)
	}
}
