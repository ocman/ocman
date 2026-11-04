package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

type cloneRoundTripHost struct {
	localStubHost
	plan, clone hostsvc.ProjectClone
	err         error
}

func (h *cloneRoundTripHost) PlanProjectClone(_ context.Context, req hostsvc.ProjectClone) (*hostsvc.ProjectClone, error) {
	h.plan = req
	if req.Directory == "" {
		req.Directory = "/owner/repo"
	}
	return &req, h.err
}
func (h *cloneRoundTripHost) CloneProject(_ context.Context, req hostsvc.ProjectClone) error {
	h.clone = req
	return h.err
}

func TestProjectCloneOwnerRoundTrip(t *testing.T) {
	owner := &cloneRoundTripHost{}
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), owner, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	req := hostsvc.ProjectClone{URL: "https://example.com/repo"}
	plan, err := host.PlanProjectClone(t.Context(), req)
	if err != nil || plan.Directory != "/owner/repo" || owner.plan != req {
		t.Fatalf("plan=%+v, %v; owner=%+v", plan, err, owner.plan)
	}
	if err := host.CloneProject(t.Context(), *plan); err != nil || owner.clone != *plan {
		t.Fatalf("clone=%+v, %v", owner.clone, err)
	}
	owner.err = errors.New("clone failed")
	if err := host.CloneProject(t.Context(), *plan); err == nil {
		t.Fatal("lost owner error")
	}
	if _, err := host.PlanProjectClone(t.Context(), req); err == nil {
		t.Fatal("lost planning error")
	}
	if err := host.CloneProject(t.Context(), hostsvc.ProjectClone{Directory: "relative"}); err == nil {
		t.Fatal("accepted relative destination")
	}
	offline := newRemoteHost(&RemoteConn{remoteID: "gone"})
	if _, err := offline.PlanProjectClone(t.Context(), req); err == nil {
		t.Fatal("offline planning did not fail closed")
	}
	if err := offline.CloneProject(t.Context(), *plan); err == nil {
		t.Fatal("offline clone did not fail closed")
	}
}

func TestProjectCloneRPCRejectsInvalidJSON(t *testing.T) {
	srv := NewServer(platforms.NewRegistry(), &cloneRoundTripHost{}, "rid", "v")
	for _, call := range []func(context.Context, *pb.CallReq) (*pb.JsonResp, error){srv.PlanProjectClone, srv.CloneProject} {
		if _, err := call(t.Context(), &pb.CallReq{Payload: []byte(`{`)}); err == nil {
			t.Fatal("accepted malformed JSON")
		}
	}
}
