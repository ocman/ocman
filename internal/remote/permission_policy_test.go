package remote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type policyTestPlatform struct{ *fakePlatform }

func (p policyTestPlatform) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	for _, session := range p.sessions {
		if session.ID == id {
			return &session, nil
		}
	}
	return nil, platforms.ErrNotFound
}

func TestRemoteSessionPermissionPolicyRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(policyTestPlatform{&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1"}}}})
	var saved []platforms.PermissionRule
	owner := NewServer(reg, nil, "owner", "test").UsePermissionPolicy(func(_ context.Context, adapter platforms.Platform, id string, rules *[]platforms.PermissionRule) (*autoapprove.PermissionPolicy, error) {
		if adapter.ID() != "opencode" || id != "s1" {
			t.Errorf("owner scope: %s %s", adapter.ID(), id)
		}
		if rules != nil {
			saved = *rules
		}
		return &autoapprove.PermissionPolicy{Rules: saved, Requests: []state.PermissionHistoryEntry{{InboxPermission: state.InboxPermission{Platform: "opencode", SessionID: id, PermissionID: "p"}}}}, nil
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
	rules := []platforms.PermissionRule{{Permission: "bash", Pattern: "tea do *", Action: "ask"}}
	result, err := adapter.SessionPermissionPolicy(t.Context(), "s1", &rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 1 || result.Requests[0].Platform != string(adapter.ID()) {
		t.Fatalf("policy = %+v", result)
	}
	result, err = adapter.SessionPermissionPolicy(t.Context(), "s1", nil)
	if err != nil || len(result.Rules) != 1 {
		t.Fatalf("read = %+v, %v", result, err)
	}
	var nilReplacement []platforms.PermissionRule
	result, err = adapter.SessionPermissionPolicy(t.Context(), "s1", &nilReplacement)
	if err != nil || len(result.Rules) != 0 {
		t.Fatalf("nil replacement retained remote rules: %+v, %v", result, err)
	}
	if _, err := adapter.SessionPermissionPolicy(t.Context(), "s1", &rules); err != nil {
		t.Fatal(err)
	}
	empty := []platforms.PermissionRule{}
	if _, err := adapter.SessionPermissionPolicy(t.Context(), "s1", &empty); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatal("empty replacement was lost in transport")
	}
	conn.Close()
	if _, err := adapter.SessionPermissionPolicy(t.Context(), "s1", &rules); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("offline write = %v", err)
	}
}

func TestRemotePermissionPolicyValidation(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(policyTestPlatform{&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1"}}}})
	srv := NewServer(reg, nil, "owner", "test")
	if _, err := srv.SessionPermissionPolicy(t.Context(), &pb.PlatformJsonReq{}); status.Code(err) != codes.Unimplemented {
		t.Fatal(err)
	}
	called := false
	srv.UsePermissionPolicy(func(context.Context, platforms.Platform, string, *[]platforms.PermissionRule) (*autoapprove.PermissionPolicy, error) {
		called = true
		return nil, errors.New("owner failed")
	})
	for _, payload := range []string{"bad", `{}`, `{"sessionId":""}`} {
		if _, err := srv.SessionPermissionPolicy(t.Context(), &pb.PlatformJsonReq{Platform: "opencode", Payload: []byte(payload)}); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(permissionPolicyRequest{SessionID: "missing"})
	if _, err := srv.SessionPermissionPolicy(t.Context(), &pb.PlatformJsonReq{Platform: "opencode", Payload: payload}); status.Code(err) != codes.NotFound {
		t.Fatal(err)
	}
	if called {
		t.Fatal("invalid session reached policy handler")
	}
	payload, _ = json.Marshal(permissionPolicyRequest{SessionID: "s1"})
	if _, err := srv.SessionPermissionPolicy(t.Context(), &pb.PlatformJsonReq{Platform: "unknown", Payload: payload}); status.Code(err) != codes.NotFound {
		t.Fatal(err)
	}
	if _, err := srv.SessionPermissionPolicy(t.Context(), &pb.PlatformJsonReq{Platform: "opencode", Payload: payload}); err == nil {
		t.Fatal("lost owner error")
	}
}
