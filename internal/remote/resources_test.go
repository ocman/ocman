package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

type resourceOwnerHost struct {
	localStubHost
	snapshot hostsvc.ResourceSnapshot
	err      error
}

func (h resourceOwnerHost) Resources(context.Context) (hostsvc.ResourceSnapshot, error) {
	return h.snapshot, h.err
}

func TestResourcesOwnerRoundTrip(t *testing.T) {
	point := hostsvc.ResourcePoint{Timestamp: 42, CPUPercent: 12.5, MemoryBytes: 2048}
	owner := resourceOwnerHost{snapshot: hostsvc.ResourceSnapshot{
		ResourcePoint: point,
		Processes:     []hostsvc.ResourceProcess{{PID: 7, ParentPID: 1, Name: "agent", Command: "agent --session-id native", CPUPercent: 12.5, MemoryBytes: 2048}},
		History:       []hostsvc.ResourcePoint{point},
	}}
	server := NewServer(platforms.NewRegistry(), owner, "remote", "test")
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", server))})
	got, err := host.Resources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Timestamp != 42 || len(got.Processes) != 1 || got.Processes[0].Command != "agent --session-id native" || len(got.History) != 1 {
		t.Fatalf("lost owner sample: %+v", got)
	}

	owner.err = errors.New("ps failed")
	failing := NewServer(platforms.NewRegistry(), owner, "remote", "test")
	host = newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", failing))})
	if _, err := host.Resources(t.Context()); err == nil {
		t.Fatal("ignored owner failure")
	}

	if _, err := newRemoteHost(&RemoteConn{}).Resources(t.Context()); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline owner did not fail closed: %v", err)
	}
}

func TestResourcesUnsupportedOwner(t *testing.T) {
	server := NewServer(platforms.NewRegistry(), localStubHost{}, "remote", "test")
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "token", server))})
	if _, err := host.Resources(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("owner without resources not classified as unsupported: %v", err)
	}
}
