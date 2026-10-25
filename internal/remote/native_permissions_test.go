package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/sessionlifecycle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nativePermissionPlatform struct {
	*fakePlatform
	nativeErr error
}

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
func (p *nativePermissionPlatform) NativePermissionState(context.Context, string) (*platforms.NativePermissionState, error) {
	return &platforms.NativePermissionState{Mode: "auto"}, p.nativeErr
}

func TestNativePermissionsRoundTrip(t *testing.T) {
	fp := &nativePermissionPlatform{fakePlatform: &fakePlatform{id: "claudecode", sessions: []db.Session{{ID: "s1"}}}}
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

func TestNativePermissionErrorsKeepRemoteRulesReadable(t *testing.T) {
	for _, want := range []error{platforms.ErrNotFound, platforms.ErrUnsupported} {
		t.Run(want.Error(), func(t *testing.T) {
			rules := []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}
			fp := &nativePermissionPlatform{fakePlatform: &fakePlatform{
				id: "claudecode", sessions: []db.Session{{ID: "s1"}}, permRules: rules,
			}, nativeErr: want}
			reg := platforms.NewRegistry()
			reg.Register(fp)
			conn := NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
			if err := conn.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(conn.Close)
			rp := newRemotePlatform(conn, "claudecode", func() string { return "Box" })
			if _, err := rp.NativePermissionState(t.Context(), "s1"); !errors.Is(err, want) {
				t.Errorf("native state lost typed error: got %v, want %v", err, want)
			}
			m := sessionlifecycle.NewModule(sessionlifecycle.Deps{
				WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, platforms.Platform)) {
					fn(w, r, "s1", rp)
				},
			})
			rr := httptest.NewRecorder()
			m.HandleSessionPermissionRulesGet(rr, httptest.NewRequest(http.MethodGet, "/api/session/s1/permission-rules", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("rules read failed: status=%d body=%s", rr.Code, rr.Body.String())
			}
			var body struct {
				Rules      []platforms.PermissionRule       `json:"rules"`
				NativeMode *platforms.NativePermissionState `json:"nativeMode"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body.Rules, rules) || body.NativeMode != nil {
				t.Fatalf("rules-only response=%s", rr.Body.String())
			}
		})
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
