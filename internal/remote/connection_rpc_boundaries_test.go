package remote

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type connectionLifecycleOwner struct {
	localStubHost
	err error
	ops []connectionOperation
}

func (h *connectionLifecycleOwner) InventoryPlatformConnections(context.Context) (hostsvc.ConnectionInventory, error) {
	return hostsvc.ConnectionInventory{}, h.err
}
func (h *connectionLifecycleOwner) CheckPlatformConnection(_ context.Context, r hostsvc.ConnectionCheckRequest) (hostsvc.ConnectionCheck, error) {
	h.ops = append(h.ops, connectionOperation{Action: "check", Check: r})
	return hostsvc.ConnectionCheck{Status: "unchecked"}, h.err
}
func (h *connectionLifecycleOwner) PreparePlatformConnection(_ context.Context, r hostsvc.ConnectionSetupRequest) (platformconnections.Reservation, error) {
	h.ops = append(h.ops, connectionOperation{Action: "prepare", Setup: r})
	return platformconnections.Reservation{ID: r.ReservationID, ConnectionID: "cnx_work"}, h.err
}
func (h *connectionLifecycleOwner) SavePlatformConnection(_ context.Context, id, name string, enabled bool) (platformconnections.Connection, error) {
	h.ops = append(h.ops, connectionOperation{Action: "save", ID: id, Name: name, Enabled: enabled})
	return platformconnections.Connection{ID: "cnx_work", Name: name, Enabled: enabled}, h.err
}
func (h *connectionLifecycleOwner) CancelPlatformConnectionSetup(_ context.Context, id string) error {
	h.ops = append(h.ops, connectionOperation{Action: "cancel", ID: id})
	return h.err
}
func (h *connectionLifecycleOwner) UpdatePlatformConnection(_ context.Context, r hostsvc.ConnectionUpdate) error {
	h.ops = append(h.ops, connectionOperation{Action: "update", Update: r})
	return h.err
}
func (h *connectionLifecycleOwner) RemovePlatformConnection(_ context.Context, id string) error {
	h.ops = append(h.ops, connectionOperation{Action: "remove", ID: id})
	return h.err
}
func (h *connectionLifecycleOwner) CompletePlatformConnectionOnboarding(context.Context) error {
	h.ops = append(h.ops, connectionOperation{Action: "onboarding"})
	return h.err
}

func TestAuthenticatedConnectionLifecycleForwardsExactOwnerReceiptAndIntent(t *testing.T) {
	owner := &connectionLifecycleOwner{}
	transport := startTestServer(t, "token", NewServer(platforms.NewRegistry(), owner, "owner", "test"))
	h := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(transport), remoteID: "owner"})
	setup := hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "isolated", ReservationID: "stp_work"}
	r, err := h.PreparePlatformConnection(t.Context(), setup)
	if err != nil || r.ID != setup.ReservationID {
		t.Fatalf("%+v %v", r, err)
	}
	c, err := h.SavePlatformConnection(t.Context(), r.ID, "Work", false)
	if err != nil || c.ID != "cnx_work" || c.Enabled {
		t.Fatalf("%+v %v", c, err)
	}
	name := "Renamed"
	update := hostsvc.ConnectionUpdate{ID: c.ID, Name: &name}
	if err := h.UpdatePlatformConnection(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if err := h.CancelPlatformConnectionSetup(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.RemovePlatformConnection(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.CompletePlatformConnectionOnboarding(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []connectionOperation{{Action: "prepare", Setup: setup}, {Action: "save", ID: r.ID, Name: "Work"}, {Action: "update", Update: update}, {Action: "cancel", ID: r.ID}, {Action: "remove", ID: c.ID}, {Action: "onboarding"}}
	if !reflect.DeepEqual(owner.ops, want) {
		t.Fatalf("owner received %+v, want %+v", owner.ops, want)
	}
}

func TestConnectionRPCSanitizesOwnerErrorsAndRejectsUnknownOperations(t *testing.T) {
	owner := &connectionLifecycleOwner{}
	s := NewServer(platforms.NewRegistry(), owner, "owner", "test")
	for _, tt := range []struct {
		err  error
		code codes.Code
	}{{platformconnections.ErrInvalid, codes.InvalidArgument}, {platformconnections.ErrNotFound, codes.NotFound}, {platformconnections.ErrUnavailable, codes.FailedPrecondition}, {platformconnections.ErrLimit, codes.FailedPrecondition}} {
		owner.err = tt.err
		for _, action := range []string{"prepare", "save", "cancel", "update", "remove", "onboarding", "check"} {
			payload, _ := marshalJSON(connectionOperation{Action: action, ID: "cnx_work"})
			_, err := s.Call(t.Context(), &pb.CallReq{Method: "PlatformConnections", Payload: payload})
			if status.Code(err) != tt.code {
				t.Fatalf("%s: %v", action, err)
			}
		}
	}
	owner.err = nil
	check, err := s.PlatformConnections(t.Context(), &pb.CallReq{Payload: []byte(`{"Action":"check"}`)})
	if err != nil || check == nil {
		t.Fatalf("%+v %v", check, err)
	}
	if _, err := s.PlatformConnections(t.Context(), &pb.CallReq{Payload: []byte(`{"Action":"secret"}`)}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := s.PlatformConnections(t.Context(), &pb.CallReq{Payload: []byte(`{`)}); err == nil {
		t.Fatal("malformed operation accepted")
	}
	unsupported := NewServer(platforms.NewRegistry(), localStubHost{}, "owner", "test")
	if _, err := unsupported.PlatformConnections(t.Context(), &pb.CallReq{}); status.Code(err) != codes.Unimplemented {
		t.Fatal(err)
	}
	offline := newRemoteHost(&RemoteConn{remoteID: "offline"})
	if err := offline.RemovePlatformConnection(t.Context(), "cnx_work"); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	if len(owner.ops) == 0 {
		t.Fatal("owner fake was not exercised")
	}
}
