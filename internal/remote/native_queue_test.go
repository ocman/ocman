package remote

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// nativeQueueFake is a remote-side platform holding follow-ups natively.
type nativeQueueFake struct {
	*fakePlatform

	mu        sync.Mutex
	held      []platforms.NativeQueuedMessage
	listErr   error
	cancelErr error
	listedFor []string
	cancelled []platforms.CancelNativeQueuedRequest
}

func (f *nativeQueueFake) NativeQueued(_ context.Context, sessionID string) ([]platforms.NativeQueuedMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listedFor = append(f.listedFor, sessionID)
	return f.held, f.listErr
}

func (f *nativeQueueFake) CancelNativeQueued(_ context.Context, req platforms.CancelNativeQueuedRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, req)
	return f.cancelErr
}

// nativeQueuePair connects a remotePlatform (hub side) to a remote Server
// whose only platform is p.
func nativeQueuePair(t *testing.T, p platforms.Platform) *remotePlatform {
	t.Helper()
	reg := platforms.NewRegistry()
	reg.Register(p)
	cc := startTestServer(t, "token", NewServer(reg, localStubHost{}, "remote", "test"))
	conn := &RemoteConn{client: pb.NewOcmanClient(cc)}
	return newRemotePlatform(conn, "opencode", func() string { return "Box" })
}

func TestRemoteNativeQueue_RoundTrip(t *testing.T) {
	want := []platforms.NativeQueuedMessage{
		{ID: "msg_1", Text: "one", HasImages: true, CreatedAt: 11},
		{ID: "msg_2", Text: "two", CreatedAt: 22},
	}
	fake := &nativeQueueFake{fakePlatform: &fakePlatform{id: "opencode"}, held: want}
	rp := nativeQueuePair(t, fake)
	var _ platforms.NativeQueue = rp

	got, err := rp.NativeQueued(t.Context(), "s1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("NativeQueued = %+v, %v; want %+v", got, err, want)
	}
	req := platforms.CancelNativeQueuedRequest{SessionID: "s1", ID: "msg_1"}
	if err := rp.CancelNativeQueued(t.Context(), req); err != nil {
		t.Fatalf("CancelNativeQueued: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !reflect.DeepEqual(fake.listedFor, []string{"s1"}) {
		t.Errorf("listed for %v, want [s1]", fake.listedFor)
	}
	if !reflect.DeepEqual(fake.cancelled, []platforms.CancelNativeQueuedRequest{req}) {
		t.Errorf("cancelled %+v, want %+v", fake.cancelled, req)
	}
}

func TestRemoteNativeQueue_EmptyList(t *testing.T) {
	rp := nativeQueuePair(t, &nativeQueueFake{fakePlatform: &fakePlatform{id: "opencode"}, held: []platforms.NativeQueuedMessage{}})
	got, err := rp.NativeQueued(t.Context(), "s1")
	if err != nil || len(got) != 0 {
		t.Fatalf("NativeQueued = %+v, %v; want empty", got, err)
	}
}

func TestRemoteNativeQueue_ErrorsCrossTheWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"unsupported", platforms.ErrUnsupported, platforms.ErrUnsupported},
		{"wrapped unsupported", errors.Join(errors.New("v1 server"), platforms.ErrUnsupported), platforms.ErrUnsupported},
		{"unreachable", platforms.ErrPlatformUnreachable, platforms.ErrPlatformUnreachable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &nativeQueueFake{fakePlatform: &fakePlatform{id: "opencode"}, listErr: tc.err, cancelErr: tc.err}
			rp := nativeQueuePair(t, fake)
			if _, err := rp.NativeQueued(t.Context(), "s1"); !errors.Is(err, tc.want) {
				t.Errorf("NativeQueued err = %v, want %v", err, tc.want)
			}
			err := rp.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{SessionID: "s1", ID: "msg_1"})
			if !errors.Is(err, tc.want) {
				t.Errorf("CancelNativeQueued err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRemoteNativeQueue_PlatformWithoutNativeQueue(t *testing.T) {
	rp := nativeQueuePair(t, &fakePlatform{id: "opencode"})
	if _, err := rp.NativeQueued(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("NativeQueued err = %v, want ErrUnsupported", err)
	}
	err := rp.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{SessionID: "s1", ID: "msg_1"})
	if !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("CancelNativeQueued err = %v, want ErrUnsupported", err)
	}
}

func TestRemoteNativeQueue_UnknownPlatformIsNotUnsupported(t *testing.T) {
	rp := nativeQueuePair(t, &fakePlatform{id: "other"})
	_, err := rp.NativeQueued(t.Context(), "s1")
	if status.Code(err) != codes.NotFound || errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("NativeQueued err = %v, want NotFound", err)
	}
	err = rp.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{SessionID: "s1"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("CancelNativeQueued err = %v, want NotFound", err)
	}
}

// A remote built before the RPC existed answers Unimplemented, which the
// hub reads as "no native queue" so it falls back to ocman's queue.
func TestRemoteNativeQueue_OldRemoteIsUnsupported(t *testing.T) {
	rp := nativeQueuePair(t, &fakePlatform{id: "opencode"})
	rp.conn = &RemoteConn{client: unimplementedClient{rp.conn.Client()}}
	if _, err := rp.NativeQueued(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("NativeQueued err = %v, want ErrUnsupported", err)
	}
}

// unimplementedClient answers NativeQueued like a server without the RPC.
type unimplementedClient struct{ pb.OcmanClient }

func (unimplementedClient) NativeQueued(context.Context, *pb.SessionRef, ...grpc.CallOption) (*pb.JsonResp, error) {
	return nil, status.Error(codes.Unimplemented, "unknown method NativeQueued")
}

func TestRemoteNativeQueue_Offline(t *testing.T) {
	rp := newRemotePlatform(&RemoteConn{}, "opencode", func() string { return "Box" })
	if _, err := rp.NativeQueued(t.Context(), "s1"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("NativeQueued offline err = %v, want ErrRemoteOffline", err)
	}
	if err := rp.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{}); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("CancelNativeQueued offline err = %v, want ErrRemoteOffline", err)
	}
}

func TestRemotePlatformError_Mapping(t *testing.T) {
	if remotePlatformError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	for _, tc := range []struct {
		code codes.Code
		want error
	}{
		{codes.Unimplemented, platforms.ErrUnsupported},
		{codes.Unavailable, platforms.ErrPlatformUnreachable},
	} {
		in := status.Error(tc.code, "x")
		got := remotePlatformError(in)
		if !errors.Is(got, tc.want) || status.Code(got) != tc.code {
			t.Errorf("remotePlatformError(%v) = %v; want %v keeping the status", tc.code, got, tc.want)
		}
	}
	other := status.Error(codes.Internal, "x")
	if got := remotePlatformError(other); !errors.Is(got, other) || errors.Is(got, platforms.ErrUnsupported) || errors.Is(got, platforms.ErrPlatformUnreachable) {
		t.Errorf("Internal must pass through unchanged, got %v", got)
	}
}

func TestSvcErr_Mapping(t *testing.T) {
	for _, tc := range []struct {
		in   error
		want codes.Code
	}{
		{platforms.ErrUnsupported, codes.Unimplemented},
		{errors.Join(errors.New("ctx"), platforms.ErrUnsupported), codes.Unimplemented},
		{platforms.ErrPlatformUnreachable, codes.Unavailable},
	} {
		if got := status.Code(svcErr(tc.in)); got != tc.want {
			t.Errorf("svcErr(%v) code = %v, want %v", tc.in, got, tc.want)
		}
	}
	if svcErr(nil) != nil {
		t.Error("svcErr(nil) must be nil")
	}
}

// sendErrFake fails every send with err.
type sendErrFake struct {
	*fakePlatform
	err error
}

func (f sendErrFake) SendMessage(context.Context, platforms.SendMessageRequest) error { return f.err }

// The hub's native enqueue sends Delivery "queue" through the remote; a
// remote whose session cannot hold it must answer ErrUnsupported so the
// hub falls back to its own queue.
func TestRemoteSendMessage_DeliveryRoundTrip(t *testing.T) {
	fake := &fakePlatform{id: "opencode"}
	rp := nativeQueuePair(t, fake)
	if err := rp.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "s1", Message: "later", Delivery: "queue"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if fake.sent == nil || fake.sent.Delivery != "queue" {
		t.Fatalf("remote received %+v, want Delivery queue", fake.sent)
	}

	rp = nativeQueuePair(t, sendErrFake{&fakePlatform{id: "opencode"}, platforms.ErrUnsupported})
	err := rp.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "s1", Message: "later", Delivery: "queue"})
	if !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("SendMessage err = %v, want ErrUnsupported", err)
	}
}
