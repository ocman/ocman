package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type historyUnavailableClient struct{ pb.OcmanClient }

func (historyUnavailableClient) Session(context.Context, *pb.SessionReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return nil, status.Error(codes.Unavailable, platforms.ErrHistoryUnavailable.Error()+": importing")
}

func TestHistoryUnavailableDoesNotDisconnectRemote(t *testing.T) {
	conn := &RemoteConn{client: historyUnavailableClient{}, health: HealthConnected, remoteID: "owner"}
	p := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	_, err := p.Session(t.Context(), "s", 30, 0)
	if !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("lost history readiness error: %v", err)
	}
	if conn.Health() != HealthConnected {
		t.Fatalf("history ingestion marked the connected owner offline: %s", conn.Health())
	}
}

func TestPurgedRemoteConversationDoesNotDisconnectOwner(t *testing.T) {
	conn := &RemoteConn{health: HealthConnected}
	err := conversationReadError(conn, status.Error(codes.NotFound, "purged"))
	if !errors.Is(err, platforms.ErrNotFound) || conn.Health() != HealthConnected {
		t.Fatal("purged history disconnected owner", err, conn.Health())
	}
}
