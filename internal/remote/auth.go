package remote

import (
	"context"
	"crypto/subtle"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authMetadataKey is the gRPC metadata key carrying the bearer token.
const authMetadataKey = "authorization"

// bearerPrefix is the standard Authorization-header scheme prefix.
const bearerPrefix = "Bearer "

// NewAuthInterceptors returns gRPC unary and stream server interceptors
// that enforce the bearer token (AD-4). A missing or wrong token is
// rejected with codes.Unauthenticated. The compare is constant-time.
func NewAuthInterceptors(token string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	check := func(ctx context.Context) error {
		if !validToken(ctx, token) {
			return status.Error(codes.Unauthenticated, "invalid or missing remote-access token")
		}
		return nil
	}
	unary := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := check(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
	stream := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := check(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
	return unary, stream
}

// validToken reports whether the request metadata carries the expected
// bearer token.
func validToken(ctx context.Context, expected string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get(authMetadataKey)
	if len(vals) == 0 {
		return false
	}
	got := vals[0]
	if len(got) > len(bearerPrefix) && got[:len(bearerPrefix)] == bearerPrefix {
		got = got[len(bearerPrefix):]
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// bearerCreds is a grpc.PerRPCCredentials that attaches the bearer token
// to every outbound call (hub side).
type bearerCreds struct {
	token      string
	requireTLS bool
}

func (c bearerCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{authMetadataKey: bearerPrefix + c.token}, nil
}

func (c bearerCreds) RequireTransportSecurity() bool { return c.requireTLS }
