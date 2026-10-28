package remote

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sidebarRPCPlatform struct {
	*fakePlatform
	query platforms.SidebarQuery
}

func (p *sidebarRPCPlatform) SidebarSessions(_ context.Context, q platforms.SidebarQuery) ([]db.Session, error) {
	p.query = q
	return p.sessions, nil
}

func TestOwnerSidebarRPCPreservesFilters(t *testing.T) {
	reg := platforms.NewRegistry()
	p := &sidebarRPCPlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "session", Pinned: true, Origin: "routine", OriginID: "r"}}}}
	reg.Register(p)
	s := NewServer(reg, localStubHost{}, "owner", "test")
	q := platforms.SidebarQuery{Directory: "/repo", Since: 100, Limit: 25, Offset: 10, Origin: "routine", OriginID: "r"}
	raw, _ := json.Marshal(q)
	resp, err := s.Sessions(t.Context(), &pb.SessionsReq{Platform: "opencode", SidebarQuery: raw})
	var rows []db.Session
	if err != nil || json.Unmarshal(resp.Payload, &rows) != nil || len(rows) != 1 || !rows[0].Pinned || !reflect.DeepEqual(p.query, q) {
		t.Fatalf("response=%+v query=%+v err=%v", resp, p.query, err)
	}
	if _, err := s.Sessions(t.Context(), &pb.SessionsReq{Platform: "opencode", SidebarQuery: []byte("{")}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	reg.Register(&fakePlatform{id: "other"})
	if _, err := s.Sessions(t.Context(), &pb.SessionsReq{Platform: "other", SidebarQuery: raw}); status.Code(err) != codes.Unimplemented {
		t.Fatal(err)
	}
}

type sidebarRPCClient struct {
	pb.OcmanClient
	req *pb.SessionsReq
	err error
}

func (c *sidebarRPCClient) Sessions(_ context.Context, req *pb.SessionsReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	c.req = req
	if c.err != nil {
		return nil, c.err
	}
	raw, _ := json.Marshal([]db.Session{{ID: "old-pinned", Directory: "/repo", TimeUpdated: 1, Pinned: true, Origin: "routine", OriginID: "r", Branch: "work"}})
	return &pb.JsonResp{Payload: raw}, nil
}

func TestRemoteSidebarKeepsPinnedRowsOutsideWindow(t *testing.T) {
	c := &sidebarRPCClient{}
	conn := &RemoteConn{client: c, health: HealthConnected, remoteID: "remote"}
	p := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	q := platforms.SidebarQuery{Directory: "/repo", Since: 100, Origin: "routine", OriginID: "r"}
	rows, err := p.SidebarSessions(t.Context(), q)
	if err != nil || len(rows) != 1 || rows[0].Platform != "r-remote:opencode" || !rows[0].Pinned || rows[0].Branch != "work" {
		t.Fatalf("remote sidebar=%+v %v", rows, err)
	}
	var got platforms.SidebarQuery
	if err := json.Unmarshal(c.req.SidebarQuery, &got); err != nil || !reflect.DeepEqual(got, q) {
		t.Fatalf("query=%+v %v", got, err)
	}
	c.err = context.Canceled
	if _, err := p.SidebarSessions(t.Context(), q); err == nil {
		t.Fatal("failed remote silently fell back")
	}
	if _, err := newRemotePlatform(&RemoteConn{}, "opencode", nil).SidebarSessions(t.Context(), q); err == nil {
		t.Fatal("disconnected owner accepted")
	}
}
