package app

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type remoteReloadPlatform struct {
	startupPlatform
	started, exited chan struct{}
}

func (p remoteReloadPlatform) ID() platforms.ID { return "claudecode" }
func (p remoteReloadPlatform) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, nil
}
func (p remoteReloadPlatform) ProxyEvents(ctx context.Context, _ string, w io.Writer, flush func()) error {
	close(p.started)
	_, _ = io.WriteString(w, "event: ready\ndata: {}\n\n")
	flush()
	<-ctx.Done()
	close(p.exited)
	return ctx.Err()
}

func TestRemoteStreamStopsWithComposition(t *testing.T) {
	store := statetest.Open(t)
	registry := platforms.NewRegistry()
	p := remoteReloadPlatform{started: make(chan struct{}), exited: make(chan struct{})}
	registry.Register(p)
	srv := server.New(nil, store, "127.0.0.1:0", registry, nil)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	listening, addr, _, stop := startRemoteServer(ctx, srv, store, state.InstanceIdentity{InstanceID: "test", RemoteToken: "test-token"}, "127.0.0.1:0", "", "", true)
	t.Cleanup(stop)
	if !listening {
		t.Fatal("remote listener did not start")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	clientCtx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("authorization", "Bearer test-token"))
	stream, err := pb.NewOcmanClient(conn).StreamEvents(clientCtx, &pb.SessionRef{Platform: "claudecode", SessionId: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()
	stop()
	select {
	case <-p.exited:
	case <-time.After(time.Second):
		t.Fatal("inbound stream outlived its composition")
	}
}
