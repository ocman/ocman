package remote

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestPluginOperationAuthenticationAndRouting(t *testing.T) {
	var calls atomic.Int32
	service := NewServer(platforms.NewRegistry(), localStubHost{}, "owner", "test").UsePlugins(func(_ context.Context, req PluginRequest) PluginResponse {
		calls.Add(1)
		if req.Operation != "configuration" || req.PluginID != "org.example.test" || req.Input.Secrets["token"] != "private" {
			t.Errorf("unexpected request: %+v", req)
		}
		return PluginResponse{Value: json.RawMessage(`{"secrets":{"token":true}}`)}
	})
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, service)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	unauth, err := grpc.NewClient(ln.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unauth.Close() })
	if _, err := pb.NewOcmanClient(unauth).PluginOperation(t.Context(), &pb.JsonReq{Payload: []byte(`{"operation":"catalog"}`)}); status.Code(err) != codes.Unauthenticated || calls.Load() != 0 {
		t.Fatalf("unauthenticated call: %v, calls=%d", err, calls.Load())
	}
	conn := NewRemoteConn("grpc://"+ln.Addr(), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	t.Cleanup(mgr.Stop)
	mr := &managedRemote{localID: 1, conn: conn}
	mgr.remotes[1] = mr
	if !mgr.publishAdapters(1, mr, newRemotePlatform(conn, "opencode", func() string { return "Owner" }), newRemoteHost(conn)) {
		t.Fatal("publish failed")
	}
	req := PluginRequest{Operation: "configuration", PluginID: "org.example.test", Input: PluginInput{Secrets: map[string]string{"token": "private"}}}
	response, err := mgr.PluginOperation(t.Context(), "owner", req)
	if err != nil || response.Error != nil || strings.Contains(string(response.Value), "private") || calls.Load() != 1 {
		t.Fatalf("round trip: %+v %v calls=%d", response, err, calls.Load())
	}
	for _, owner := range []string{"local", "missing"} {
		if _, err := mgr.PluginOperation(t.Context(), owner, req); !errors.Is(err, ErrRemoteOffline) {
			t.Fatalf("%s: %v", owner, err)
		}
	}
	conn.Close()
	if _, err := mgr.PluginOperation(t.Context(), "owner", req); !errors.Is(err, ErrRemoteOffline) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("disconnected request dispatched")
	}
}

func TestPluginOperationInputValidation(t *testing.T) {
	s := NewServer(nil, nil, "owner", "test")
	if _, err := s.PluginOperation(t.Context(), &pb.JsonReq{}); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	s.UsePlugins(func(context.Context, PluginRequest) PluginResponse {
		t.Fatal("invalid operation reached callback")
		return PluginResponse{}
	})
	for _, payload := range []string{"{", `{"unknown":"secret"}`, `{} {}`, strings.Repeat(" ", maxPluginRequestBytes+1)} {
		if _, err := s.PluginOperation(t.Context(), &pb.JsonReq{Payload: []byte(payload)}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("malformed request: %v", err)
		}
	}
	for _, operation := range []string{"actions", "invoke"} {
		for _, owners := range [][2]string{{"wrong", "owner"}, {"owner", "wrong"}} {
			payload, _ := json.Marshal(PluginRequest{Operation: operation, Action: plugins.ActionRequest{OwnerID: owners[0], Context: plugins.ActionContext{OwnerID: owners[1]}}})
			resp, err := s.PluginOperation(t.Context(), &pb.JsonReq{Payload: payload})
			if err != nil || !strings.Contains(string(resp.Payload), string(plugins.ErrorPermissionDenied)) {
				t.Fatalf("owner mismatch: %v %v", resp, err)
			}
		}
	}
	conn := NewRemoteConn("grpc://127.0.0.1:1", "tok")
	if _, err := conn.PluginOperation(t.Context(), PluginRequest{}); !errors.Is(err, ErrRemoteOffline) {
		t.Fatal(err)
	}
}
