package remote

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Apply after authentication on both services, including streams: no remote
// consumer may observe or mutate session state during identity cutover.
func readinessInterceptors(ready func() bool) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	check := func() error {
		if ready != nil && !ready() {
			return status.Error(codes.Unavailable, "session data is updating; retry after preparation")
		}
		return nil
	}
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
			if err := check(); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}, func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
			if err := check(); err != nil {
				return err
			}
			return next(srv, stream)
		}
}
