package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func (localStubHost) CreateOneOffDirectory(context.Context) (string, error) {
	return "/owner/tmp/one-off", nil
}

type oneOffHost struct {
	localStubHost
	err error
}

func (h *oneOffHost) CreateOneOffDirectory(context.Context) (string, error) {
	return "/owner/tmp/one-off", h.err
}

func TestOneOffDirectoryOwnerRoundTrip(t *testing.T) {
	owner := &oneOffHost{}
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), owner, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	path, err := host.CreateOneOffDirectory(t.Context())
	if err != nil || path != "/owner/tmp/one-off" {
		t.Fatalf("path=%q, err=%v", path, err)
	}
	owner.err = errors.New("allocation failed")
	if _, err := host.CreateOneOffDirectory(t.Context()); err == nil {
		t.Fatal("lost owner allocation error")
	}
	offline := newRemoteHost(&RemoteConn{remoteID: "gone"})
	if _, err := offline.CreateOneOffDirectory(t.Context()); err == nil {
		t.Fatal("disconnected owner did not fail closed")
	}
}
