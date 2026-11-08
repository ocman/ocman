package remote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRemoteSessionAutoApproveRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(policyTestPlatform{&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1"}}}})
	stored := autoapprove.AutoApproveState{}
	owner := NewServer(reg, nil, "owner", "test").UseAutoApprove(func(_ context.Context, adapter platforms.Platform, id string, enabled *bool) (autoapprove.AutoApproveState, error) {
		if adapter.ID() != "opencode" || id != "s1" {
			t.Errorf("owner scope: %s %s", adapter.ID(), id)
		}
		if enabled != nil {
			stored = autoapprove.AutoApproveState{Enabled: *enabled, Overridden: true}
		}
		return stored, nil
	})
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, owner)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	conn := NewRemoteConn("grpc://"+ln.Addr(), "token")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	adapter := newRemotePlatform(conn, "opencode", func() string { return "owner" })

	if got, err := adapter.SessionAutoApprove(t.Context(), "s1", nil); err != nil || got.Overridden {
		t.Fatalf("initial read = %+v %v", got, err)
	}
	on := true
	if got, err := adapter.SessionAutoApprove(t.Context(), "s1", &on); err != nil || !got.Enabled || !got.Overridden {
		t.Fatalf("write = %+v %v", got, err)
	}
	off := false
	if got, err := adapter.SessionAutoApprove(t.Context(), "s1", &off); err != nil || got.Enabled || !got.Overridden {
		t.Fatalf("disable = %+v %v", got, err)
	}
	conn.Close()
	if _, err := adapter.SessionAutoApprove(t.Context(), "s1", &on); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline write = %v", err)
	}
}

func TestRemoteSessionAutoApproveValidation(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(policyTestPlatform{&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1"}}}})
	reg.Register(&fakePlatform{id: "r-other:opencode"})
	srv := NewServer(reg, nil, "owner", "test")
	if _, err := srv.SessionAutoApprove(t.Context(), &pb.CallReq{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("without handler = %v", err)
	}
	called := false
	srv.UseAutoApprove(func(context.Context, platforms.Platform, string, *bool) (autoapprove.AutoApproveState, error) {
		called = true
		return autoapprove.AutoApproveState{}, errors.New("owner failed")
	})
	for _, payload := range []string{"bad", `{}`, `{"sessionId":""}`} {
		if _, err := srv.SessionAutoApprove(t.Context(), &pb.CallReq{Platform: "opencode", Payload: []byte(payload)}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("payload %q = %v", payload, err)
		}
	}
	payload, _ := json.Marshal(autoApproveRequest{SessionID: "missing"})
	if _, err := srv.SessionAutoApprove(t.Context(), &pb.CallReq{Platform: "opencode", Payload: payload}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing session = %v", err)
	}
	payload, _ = json.Marshal(autoApproveRequest{SessionID: "s1"})
	if _, err := srv.SessionAutoApprove(t.Context(), &pb.CallReq{Platform: "unknown", Payload: payload}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown platform = %v", err)
	}
	if _, err := srv.SessionAutoApprove(t.Context(), &pb.CallReq{Platform: "r-other:opencode", Payload: payload}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("relayed platform = %v", err)
	}
	if called {
		t.Fatal("invalid request reached the handler")
	}
	if _, err := srv.SessionAutoApprove(t.Context(), &pb.CallReq{Platform: "opencode", Payload: payload}); err == nil || !called {
		t.Fatal("lost owner error")
	}
}
