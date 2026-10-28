package remote

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestLifetimeInterceptorCancelsUnaryContext(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	unary, _ := lifetimeInterceptors(parent)
	_, err := unary(t.Context(), nil, nil, func(ctx context.Context, _ any) (any, error) {
		cancel()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("unary context outlived owner")
		}
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = unary(t.Context(), nil, nil, func(ctx context.Context, _ any) (any, error) { return nil, ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("RPC admitted with live context after owner stopped")
	}
}

type backpressuredPlatform struct {
	platforms.Platform
	exited chan struct{}
}

func (backpressuredPlatform) ID() platforms.ID { return "fake" }
func (p backpressuredPlatform) ProxyEvents(_ context.Context, _ string, w io.Writer, _ func()) error {
	defer close(p.exited)
	frame := "event: ready\ndata: " + strings.Repeat("x", 64<<10) + "\n\n"
	for {
		if _, err := io.WriteString(w, frame); err != nil {
			return err
		}
	}
}

func TestStopFallsBackForBackpressuredStreamAndJoinsHandler(t *testing.T) {
	registry := platforms.NewRegistry()
	p := backpressuredPlatform{exited: make(chan struct{})}
	registry.Register(p)
	listener, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "test", TrustedOverlay: true}, NewServer(registry, nil, "test", "test"))
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() { _ = listener.Serve(); close(served) }()
	t.Cleanup(listener.Stop)
	conn, err := grpc.NewClient(listener.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("authorization", "Bearer test"))
	stream, err := pb.NewOcmanClient(conn).StreamEvents(ctx, &pb.SessionRef{Platform: "fake", SessionId: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	// Stop reading so transport flow control can hold SendMsg even though the
	// handler's owner context has been canceled. The force-stop releases it.
	listener.stopWithin(time.Millisecond)
	select {
	case <-p.exited:
	default:
		t.Fatal("stop returned before handler cleanup")
	}
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("gRPC Serve did not stop")
	}
}
