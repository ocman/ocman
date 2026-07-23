package remote

import (
	"context"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

type beadsRoundTripHost struct {
	localStubHost
	dir string
}

func (h *beadsRoundTripHost) BeadsStatus(_ context.Context, dir string) (hostsvc.BeadsStatus, error) {
	h.dir = dir
	return hostsvc.BeadsStatus{Available: true, Tickets: []hostsvc.BeadsTicket{
		{ID: "bd-1", Title: "Parent", Status: "open", Priority: 1},
		{ID: "bd-2", Title: "Child", Status: "blocked", Priority: 2, ParentID: "bd-1"},
	}}, nil
}

func TestRemoteHostBeadsStatusRoundTrip(t *testing.T) {
	owner := &beadsRoundTripHost{}
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), owner, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})

	got, err := host.BeadsStatus(context.Background(), "/remote/repo")
	if err != nil {
		t.Fatal(err)
	}
	if owner.dir != "/remote/repo" {
		t.Fatalf("owner got directory %q", owner.dir)
	}
	want := hostsvc.BeadsStatus{Available: true, Tickets: []hostsvc.BeadsTicket{
		{ID: "bd-1", Title: "Parent", Status: "open", Priority: 1},
		{ID: "bd-2", Title: "Child", Status: "blocked", Priority: 2, ParentID: "bd-1"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
}
