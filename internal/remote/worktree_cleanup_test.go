package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type maintenanceHost struct {
	localStubHost
	cleaned  hostsvc.CleanupWorktreeRequest
	restored hostsvc.WorktreeCheckpoint
	err      error
}

func (h *maintenanceHost) CleanupWorktree(_ context.Context, req hostsvc.CleanupWorktreeRequest) error {
	h.cleaned = req
	return h.err
}
func (h *maintenanceHost) RestoreWorktree(_ context.Context, checkpoint hostsvc.WorktreeCheckpoint) error {
	h.restored = checkpoint
	return h.err
}

func TestWorktreeMaintenanceOwnerRoundTrip(t *testing.T) {
	owner := &maintenanceHost{}
	server := NewServer(platforms.NewRegistry(), owner, "remote", "test")
	conn := &RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", server))}
	host := newRemoteHost(conn)
	req := hostsvc.CleanupWorktreeRequest{WorktreeCheckpoint: hostsvc.WorktreeCheckpoint{Project: "/repo", Path: "/managed/wt", Branch: "retained", Head: "abc"}, Sessions: []hostsvc.WorktreeSessionCheck{{Platform: "fake", SessionID: "s", TimeUpdated: 123}}}
	if err := host.CleanupWorktree(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if owner.cleaned.Path != req.Path || len(owner.cleaned.Sessions) != 1 || owner.cleaned.Sessions[0].SessionID != "s" {
		t.Fatalf("lost safety snapshot: %+v", owner.cleaned)
	}
	if err := host.RestoreWorktree(t.Context(), req.WorktreeCheckpoint); err != nil {
		t.Fatal(err)
	}
	if owner.restored != req.WorktreeCheckpoint {
		t.Fatalf("lost checkpoint: %+v", owner.restored)
	}
	owner.err = errors.New("dirty worktree")
	if err := host.CleanupWorktree(t.Context(), req); err == nil {
		t.Fatal("ignored owner refusal")
	}
	if err := host.RestoreWorktree(t.Context(), req.WorktreeCheckpoint); err == nil {
		t.Fatal("ignored restore refusal")
	}
	offline := newRemoteHost(&RemoteConn{})
	if err := offline.CleanupWorktree(t.Context(), req); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	if err := offline.RestoreWorktree(t.Context(), req.WorktreeCheckpoint); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
}

func TestWorktreeMaintenanceRejectsUnsupportedOwnerAndMalformedInput(t *testing.T) {
	server := NewServer(platforms.NewRegistry(), localStubHost{}, "remote", "test")
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", server))})
	if err := host.CleanupWorktree(t.Context(), hostsvc.CleanupWorktreeRequest{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("older owner unsupported response not classified: %v", err)
	}
	for _, method := range []string{"CleanupWorktree", "RestoreWorktree"} {
		if _, err := server.Call(t.Context(), &pb.CallReq{Method: method, Payload: []byte("{")}); err == nil {
			t.Fatalf("%s accepted invalid payload", method)
		}
		if _, err := server.Call(t.Context(), &pb.CallReq{Method: method, Payload: []byte("{}")}); !errors.Is(err, platforms.ErrUnsupported) && status.Code(err) != codes.Unimplemented {
			t.Fatalf("%s bypassed unsupported owner: %v", method, err)
		}
	}
}
