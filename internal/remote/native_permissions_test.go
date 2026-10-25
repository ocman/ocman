package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nativePermissionPlatform struct{ *fakePlatform }

func (*nativePermissionPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{PermissionRules: true, NativeAutoPermission: true}
}

type legacyPermissionClient struct {
	pb.OcmanClient
	rulesWrites int
}

func (c *legacyPermissionClient) Call(_ context.Context, req *pb.CallReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	if req.Method == "SetPermissionRules" {
		c.rulesWrites++ // an older owner would ignore NativeMode and clear the rules
		return &pb.JsonResp{}, nil
	}
	return nil, status.Error(codes.Unimplemented, "older owner")
}

func TestNativeModeNeverFallsBackToLegacyRulesWrite(t *testing.T) {
	client := &legacyPermissionClient{}
	rp := newRemotePlatform(&RemoteConn{client: client}, "claudecode", func() string { return "Box" })
	auto := "auto"
	err := rp.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s1", NativeMode: &auto})
	if err == nil || client.rulesWrites != 0 {
		t.Fatalf("legacy owner accepted native mode as a rules reset: writes=%d err=%v", client.rulesWrites, err)
	}
	if err := rp.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s1"}); err != nil || client.rulesWrites != 1 {
		t.Fatalf("ordinary rules write changed: writes=%d err=%v", client.rulesWrites, err)
	}
}
func (*nativePermissionPlatform) NativePermissionState(context.Context, string) (*platforms.NativePermissionState, error) {
	return &platforms.NativePermissionState{Mode: "auto"}, nil
}

func TestNativePermissionsRoundTrip(t *testing.T) {
	fp := &nativePermissionPlatform{&fakePlatform{id: "claudecode", sessions: []db.Session{{ID: "s1"}}}}
	reg := platforms.NewRegistry()
	reg.Register(fp)
	addr := startRealRemote(t, "tok", "rid", reg)
	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	rp := newRemotePlatform(conn, "claudecode", func() string { return "Box" })
	state, err := rp.NativePermissionState(t.Context(), "s1")
	if err != nil || state.Mode != "auto" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	auto := "auto"
	if err := rp.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s1", NativeMode: &auto}); err != nil {
		t.Fatal(err)
	}
	if fp.setPermReq == nil || fp.setPermReq.NativeMode == nil || *fp.setPermReq.NativeMode != "auto" || fp.setPermReq.Rules != nil {
		t.Fatalf("request=%+v", fp.setPermReq)
	}
	conn.Close()
	if _, err := rp.NativePermissionState(t.Context(), "s1"); err == nil {
		t.Fatal("disconnected owner fell back")
	}
}

func TestNativePermissionsUnsupportedRemote(t *testing.T) {
	conn := connectedPair(t, &fakePlatform{id: "opencode"})
	t.Cleanup(conn.Close)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	if _, err := rp.NativePermissionState(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}
