package remote

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type countHealthClient struct {
	pb.OcmanClient
	err error
}

func (c countHealthClient) Call(ctx context.Context, _ *pb.CallReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return nil, c.err
}

func TestRunningCountCallerCancellationPreservesRemoteHealth(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   codes.Code
		health Health
	}{
		{"cancelled", codes.Canceled, HealthConnected},
		{"expired", codes.DeadlineExceeded, HealthConnected},
		{"unsupported", codes.Unimplemented, HealthConnected},
		{"transport failed", codes.Unavailable, HealthOffline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			switch tc.code {
			case codes.Canceled:
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case codes.DeadlineExceeded:
				expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				ctx = expired
			}
			conn := &RemoteConn{client: countHealthClient{err: status.Error(tc.code, tc.name)}, health: HealthConnected, remoteID: "owner"}
			p := newRemotePlatform(conn, "test", nil)
			if _, err := p.RunningSessionCount(ctx, platforms.SidebarQuery{}); status.Code(err) != tc.code {
				t.Fatalf("count error = %v, want %s", err, tc.code)
			}
			if got := conn.Health(); got != tc.health || p.Available(t.Context()) != (tc.health == HealthConnected) {
				t.Fatalf("request changed shared owner availability: health=%s want=%s", got, tc.health)
			}
		})
	}
}
