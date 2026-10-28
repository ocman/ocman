package remote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCanonicalReadRPCsRejectNativeAndPendingBeforeAdapterCalls(t *testing.T) {
	s := canonicalRPCServer(t)
	reads := map[string]func(context.Context, *pb.CallReq) (*pb.JsonResp, error){
		"changes": s.SessionChanges, "info": s.SessionInfo, "agents": s.AgentCatalog, "commands": s.SlashCommands, "models": s.SessionModels,
		"permissions": s.ListPermissions, "refresh permissions": s.RefreshPermissions, "questions": s.ListQuestions, "queue": s.NativeQueued, "rules": s.PermissionRules,
		"summary": s.SessionSummary, "lifecycle": s.SessionLifecycle, "usage": s.SessionUsage,
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				id   string
				code codes.Code
			}{{"native", codes.InvalidArgument}, {identity.PendingReference(), codes.Unavailable}, {"", codes.InvalidArgument}} {
				if _, err := read(t.Context(), &pb.CallReq{Platform: "opencode", SessionId: tc.id}); status.Code(err) != tc.code {
					t.Errorf("%q reached read adapter: %v", tc.id, err)
				}
			}
		})
	}
	if _, err := s.Session(t.Context(), &pb.SessionReq{Platform: "opencode", SessionId: "native"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("native transcript request accepted: %v", err)
	}
	s.UsePermissionPolicy(func(context.Context, platforms.Platform, string, *[]platforms.PermissionRule) (*autoapprove.PermissionPolicy, error) {
		t.Fatal("unproven reference reached policy mutation")
		return nil, nil
	})
	for _, ref := range []struct {
		id   string
		code codes.Code
	}{{"native", codes.InvalidArgument}, {identity.PendingReference(), codes.Unavailable}} {
		payload, _ := json.Marshal(permissionPolicyRequest{SessionID: ref.id})
		if _, err := s.SessionPermissionPolicy(t.Context(), &pb.CallReq{Platform: "opencode", Payload: payload}); status.Code(err) != ref.code {
			t.Fatalf("policy accepted unproven reference: %v", err)
		}
		if err := s.StreamEvents(&pb.SessionRef{Platform: "opencode", SessionId: ref.id}, nil); status.Code(err) != ref.code {
			t.Fatalf("stream accepted unproven reference: %v", err)
		}
	}
	if _, err := s.eventPlatformFor("opencode", ""); err != nil {
		t.Fatalf("owner-global canonical stream rejected: %v", err)
	}
	if _, err := s.sessionPlatformFor("opencode", "ocm_"+uuid.NewString()); err != nil {
		t.Fatalf("canonical read reference rejected: %v", err)
	}
}
