package remote

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReadinessFencesBothUnaryAndStreamingConsumers(t *testing.T) {
	for _, ready := range []func() bool{nil, func() bool { return false }, func() bool { return true }} {
		unary, stream := readinessInterceptors(ready)
		allowed := ready == nil || ready()
		called := false
		_, err := unary(t.Context(), nil, nil, func(context.Context, any) (any, error) { called = true; return nil, nil })
		if called != allowed || !allowed && status.Code(err) != codes.Unavailable {
			t.Fatalf("unary admission: allowed=%v called=%v err=%v", allowed, called, err)
		}
		called = false
		err = stream(nil, nil, nil, func(any, grpc.ServerStream) error { called = true; return nil })
		if called != allowed || !allowed && status.Code(err) != codes.Unavailable {
			t.Fatalf("stream admission: allowed=%v called=%v err=%v", allowed, called, err)
		}
	}
}
