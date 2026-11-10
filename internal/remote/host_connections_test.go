package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

type connectionOwner struct {
	localStubHost
	hostsvc.ConnectionHost
}

func (h *connectionOwner) InventoryPlatformConnections(context.Context) (hostsvc.ConnectionInventory, error) {
	return hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_owner", Kind: "codex", Status: "unchecked"}}}, nil
}
func (h *connectionOwner) CheckPlatformConnection(context.Context, hostsvc.ConnectionCheckRequest) (hostsvc.ConnectionCheck, error) {
	return hostsvc.ConnectionCheck{ReservationID: "stp_kept"}, platformconnections.ErrUnavailable
}

func TestConnectionInventoryAndReceiptOverRPC(t *testing.T) {
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), &connectionOwner{}, "rid", "v"))
	h := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	got, err := h.InventoryPlatformConnections(t.Context())
	if err != nil || !got.Supported || len(got.Connections) != 1 || got.Connections[0].ID != "cnx_owner" {
		t.Fatalf("%+v %v", got, err)
	}
	check, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{Kind: "codex", Configuration: "isolated"})
	if err == nil || check.ReservationID != "stp_kept" {
		t.Fatalf("%+v %v", check, err)
	}
	offline := newRemoteHost(&RemoteConn{remoteID: "offline"})
	if _, err := offline.InventoryPlatformConnections(t.Context()); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
}
