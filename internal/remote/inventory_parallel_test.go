package remote

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func TestRefreshInventoriesOverlapsRemoteReads(t *testing.T) {
	for _, count := range []int{2, inventoryRefreshParallelism + 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			testInventoryRefreshOverlap(t, count)
		})
	}
}

func testInventoryRefreshOverlap(t *testing.T, count int) {
	t.Helper()
	resume := make(chan struct{})
	defer close(resume)
	m, started := blockedInventoryManager(t, count, resume)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		m.RefreshInventories(ctx)
		close(done)
	}()
	for range min(count, inventoryRefreshParallelism) {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("remote inventory reads did not overlap")
		}
	}
	// Release the RPCs and verify the joined refresh populated both owners.
	for range count {
		resume <- struct{}{}
	}
	<-done
	if got := len(m.RemoteProjects()); got != count {
		t.Fatalf("got %d projects, want %d owners", got, count)
	}
}

func TestRefreshInventoriesBoundsConcurrencyAndJoinsOnCancellation(t *testing.T) {
	resume := make(chan struct{})
	defer close(resume)
	m, started := blockedInventoryManager(t, inventoryRefreshParallelism+2, resume)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		m.RefreshInventories(ctx)
		close(done)
	}()
	for range inventoryRefreshParallelism {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("exceeded the inventory concurrency limit")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh did not join workers after cancellation")
	}
	if got := len(m.RemoteProjects()); got != 0 {
		t.Fatalf("cancelled reads published %d projects", got)
	}
}

func TestRefreshInventoriesWithoutRemotes(t *testing.T) {
	newInvManager(t).RefreshInventories(t.Context())
}

func blockedInventoryManager(t *testing.T, count int, resume chan struct{}) (*Manager, <-chan struct{}) {
	t.Helper()
	m := newInvManager(t)
	started := make(chan struct{}, count)
	for i := range count {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := blockedInventoryServer{
			Server:  NewServer(platforms.NewRegistry(), localStubHost{}, fmt.Sprintf("owner-%d", i), "test"),
			started: make(chan struct{}), resume: resume,
		}
		server := grpc.NewServer()
		pb.RegisterOcmanServer(server, srv)
		go func() { _ = server.Serve(ln) }()
		t.Cleanup(server.Stop)
		conn := NewRemoteConn("grpc://"+ln.Addr().String(), "")
		if err := conn.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(conn.Close)
		id := int64(i + 1)
		m.remotes[id] = &managedRemote{localID: id, conn: conn, host: newRemoteHost(conn)}
		go func() {
			select {
			case <-srv.started:
				started <- struct{}{}
			case <-t.Context().Done():
			}
		}()
	}
	return m, started
}
