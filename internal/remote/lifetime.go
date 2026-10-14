package remote

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

type lifetimeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s lifetimeStream) Context() context.Context { return s.ctx }

func lifetimeInterceptors(parent context.Context) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	bind := func(ctx context.Context) (context.Context, func()) {
		child, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(parent, cancel)
		if parent.Err() != nil {
			cancel()
		}
		return child, func() { stop(); cancel() }
	}
	unary := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, stop := bind(ctx)
		defer stop()
		return handler(ctx, req)
	}
	stream := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, stop := bind(ss.Context())
		defer stop()
		return handler(srv, lifetimeStream{ServerStream: ss, ctx: ctx})
	}
	return unary, stream
}

func (l *Listener) stopWithin(timeout time.Duration) {
	l.cancel()
	done := make(chan struct{})
	go func() { l.grpc.GracefulStop(); close(done) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		l.grpc.Stop()
		<-done
	}
}
