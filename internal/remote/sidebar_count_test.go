package remote

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type aggregateRPCPlatform struct{ *sidebarRPCPlatform }

func (p *aggregateRPCPlatform) RunningSessionCount(_ context.Context, q platforms.SidebarQuery) (int, error) {
	p.query = q
	return 9, nil
}

func TestOwnerRunningCountRPC(t *testing.T) {
	reg := platforms.NewRegistry()
	p := &aggregateRPCPlatform{&sidebarRPCPlatform{fakePlatform: &fakePlatform{id: "test"}}}
	reg.Register(p)
	s := NewServer(reg, localStubHost{}, "owner", "test")
	q := platforms.SidebarQuery{Directory: "/repo", Origin: "routine", OriginID: "r"}
	raw, _ := json.Marshal(q)
	response, err := s.Call(t.Context(), &pb.CallReq{Method: "RunningSessionCount", Platform: "test", Payload: raw})
	if err != nil || string(response.Payload) != `{"count":9}` || !reflect.DeepEqual(p.query, q) {
		t.Fatalf("response=%v query=%+v err=%v", response, p.query, err)
	}
	reg.Register(&fakePlatform{id: "unsupported"})
	if _, err := s.Call(t.Context(), &pb.CallReq{Method: "RunningSessionCount", Platform: "unsupported", Payload: raw}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unsupported aggregate: %v", err)
	}
	if _, err := s.RunningSessionCount(t.Context(), &pb.CallReq{Platform: "test", Payload: []byte("{")}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid count query: %v", err)
	}
	if _, err := s.RunningSessionCount(t.Context(), &pb.CallReq{Platform: "missing", Payload: raw}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown count owner: %v", err)
	}
}

func TestRunningCountAuthenticatedOwnerRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&aggregateRPCPlatform{&sidebarRPCPlatform{fakePlatform: &fakePlatform{id: "test"}}})
	listener, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "test-token", TrustedOverlay: true}, NewServer(reg, localStubHost{}, "owner", "test"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	conn := NewRemoteConn("grpc://"+listener.Addr(), "test-token")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	p := newRemotePlatform(conn, "test", nil)
	if n, err := p.RunningSessionCount(t.Context(), platforms.SidebarQuery{}); err != nil || n != 9 {
		t.Fatalf("owner aggregate=%d %v", n, err)
	}
	unauthenticated, err := grpc.NewClient(listener.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unauthenticated.Close() })
	if _, err := pb.NewOcmanClient(unauthenticated).Call(t.Context(), &pb.CallReq{Method: "RunningSessionCount", Platform: "test", Payload: []byte("{}")}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated count: %v", err)
	}
	conn.Close()
	if _, err := p.RunningSessionCount(t.Context(), platforms.SidebarQuery{}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("disconnected owner accepted count: %v", err)
	}
}

type countRPCClient struct {
	pb.OcmanClient
	payload string
	err     error
	req     *pb.CallReq
}

func (c *countRPCClient) Call(_ context.Context, req *pb.CallReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	c.req = req
	return &pb.JsonResp{Payload: []byte(c.payload)}, c.err
}

func TestRemoteRunningCountNeverFallsBackToRows(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		err           error
		wantErr       bool
	}{
		{"aggregate", `{"count":7}`, nil, false},
		{"old peer", `[{"status":"busy"}]`, nil, true},
		{"invalid", `{`, nil, true},
		{"missing count", `{}`, nil, true},
		{"old unsupported peer", "", status.Error(codes.Unimplemented, "unknown remote method"), true},
		{"negative", `{"count":-1}`, nil, true},
		{"offline", "", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &countRPCClient{payload: tc.payload, err: tc.err}
			p := newRemotePlatform(&RemoteConn{client: client, health: HealthConnected, remoteID: "remote"}, "test", nil)
			n, err := p.RunningSessionCount(t.Context(), platforms.SidebarQuery{Directory: "/repo", Origin: "routine", Limit: 100})
			if (err != nil) != tc.wantErr || (!tc.wantErr && n != 7) {
				t.Fatalf("count=%d err=%v", n, err)
			}
			var q platforms.SidebarQuery
			if err := json.Unmarshal(client.req.Payload, &q); err != nil || client.req.Method != "RunningSessionCount" || q.Directory != "/repo" || q.Origin != "routine" || q.Limit != 0 || client.req.Platform != "test" {
				t.Fatalf("owner query=%+v req=%+v err=%v", q, client.req, err)
			}
		})
	}
	if _, err := newRemotePlatform(&RemoteConn{}, "test", nil).RunningSessionCount(t.Context(), platforms.SidebarQuery{}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("disconnected owner: %v", err)
	}
}
