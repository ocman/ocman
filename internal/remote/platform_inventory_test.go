package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
)

type inventoryRPCClient struct {
	pb.OcmanClient
	request *pb.SessionsReq
	payload []byte
	err     error
}

func (c *inventoryRPCClient) Sessions(_ context.Context, req *pb.SessionsReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	c.request = req
	return &pb.JsonResp{Payload: c.payload}, c.err
}

func TestRemoteSessionInventoryNeverFallsBack(t *testing.T) {
	for _, cached := range [][]db.Session{nil, {{ID: "cached", Directory: "/repo"}}} {
		client := &inventoryRPCClient{err: errors.New("RPC failed")}
		p := newRemotePlatform(&RemoteConn{client: client, health: HealthConnected, remoteID: "box"}, "opencode", func() string { return "Box" })
		p.lastSess = cached
		if rows, err := p.SessionInventory(t.Context(), "/repo"); err == nil || rows != nil {
			t.Fatalf("RPC failure became inventory: %+v %v", rows, err)
		}
	}
	p := newRemotePlatform(&RemoteConn{}, "opencode", nil)
	if rows, err := p.SessionInventory(t.Context(), "/repo"); !errors.Is(err, platforms.ErrRemoteOffline) || rows != nil {
		t.Fatalf("offline inventory: %+v %v", rows, err)
	}
}

func TestRemoteSessionInventoryValidatesAndScopesReplies(t *testing.T) {
	for _, tc := range []struct {
		payload         string
		canonical, fail bool
		count           int
	}{
		{`[]`, false, false, 0},
		{`[{"id":"s1","directory":"/repo"}]`, false, false, 1},
		{`not JSON`, false, true, 0},
		{`[{"id":"native","directory":"/repo"}]`, true, true, 0},
	} {
		client := &inventoryRPCClient{payload: []byte(tc.payload)}
		version := 0
		if tc.canonical {
			version = canonicalSessionProtocol
		}
		p := newRemotePlatform(&RemoteConn{client: client, health: HealthConnected, remoteID: "box", protocolVersion: version}, "cnx_work", func() string { return "Box" })
		rows, err := p.SessionInventory(t.Context(), "/repo")
		if (err != nil) != tc.fail || len(rows) != tc.count {
			t.Fatalf("inventory %q: %+v %v", tc.payload, rows, err)
		}
		if client.request.Platform != "cnx_work" || client.request.Dir != "/repo" || client.request.Since != 0 {
			t.Fatalf("incorrect scope: %+v", client.request)
		}
		if len(rows) > 0 && (rows[0].Platform != "r-box:cnx_work" || rows[0].RemoteID != "box" || rows[0].Stale) {
			t.Fatalf("inventory not owner stamped: %+v", rows)
		}
	}
}
