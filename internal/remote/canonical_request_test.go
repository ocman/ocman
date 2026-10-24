package remote

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type canonicalRPCPlatform struct{ *fakePlatform }

func (*canonicalRPCPlatform) CanonicalSessionIDs() bool { return true }

func canonicalRPCServer(t *testing.T) *Server {
	t.Helper()
	registry := platforms.NewRegistry()
	registry.Register(&canonicalRPCPlatform{fakePlatform: &fakePlatform{id: "opencode"}})
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	return NewServer(registry, localStubHost{}, "owner", "test")
}

func TestCanonicalSessionRPCContractsRejectNativeAndPendingReferences(t *testing.T) {
	s := canonicalRPCServer(t)
	constructors := []func(string) any{
		func(id string) any {
			return &platforms.SendMessageRequest{SessionID: id, Message: `{"sessionID":"opaque-native"}`}
		},
		func(id string) any { return &platforms.ExecuteCommandRequest{SessionID: id} },
		func(id string) any { return &platforms.RunShellRequest{SessionID: id} },
		func(id string) any { return &platforms.RespondPermissionRequest{SessionID: id} },
		func(id string) any { return &platforms.RespondQuestionRequest{SessionID: id} },
		func(id string) any { return &platforms.RejectQuestionRequest{SessionID: id} },
		func(id string) any { return &platforms.CancelNativeQueuedRequest{SessionID: id} },
		func(id string) any { return &platforms.AbortRequest{SessionID: id} },
		func(id string) any { return &platforms.RenameSessionRequest{SessionID: id} },
		func(id string) any { return &platforms.SetPermissionRulesRequest{SessionID: id} },
		func(id string) any { return &platforms.CompactRequest{SessionID: id} },
		func(id string) any { return &platforms.MoveSessionRequest{SessionID: id} },
		func(id string) any { return &platforms.DisposeSessionRequest{SessionID: id} },
		func(id string) any { return &platforms.ForkSessionRequest{SessionID: id, MessageID: "native-message"} },
	}
	for _, construct := range constructors {
		for _, ref := range []struct {
			id   string
			code codes.Code
		}{{"native", codes.InvalidArgument}, {identity.PendingReference(), codes.Unavailable}, {"ocm_" + uuid.NewString(), codes.OK}} {
			encoded, err := marshalJSON(construct(ref.id))
			if err != nil {
				t.Fatal(err)
			}
			dst := construct("")
			err = s.decodeSessionRequest(&pb.CallReq{Platform: "opencode", Payload: encoded}, dst)
			if status.Code(err) != ref.code {
				t.Fatalf("contract %T reference %s: %v", dst, ref.id, err)
			}
			if sent, ok := dst.(*platforms.SendMessageRequest); ok && sent.Message != `{"sessionID":"opaque-native"}` {
				t.Fatal("RPC validation rewrote opaque prompt")
			}
		}
	}
}

func TestCanonicalCreationRPCValidatesParentBeforeHostSideEffects(t *testing.T) {
	s := canonicalRPCServer(t)
	for _, ref := range []struct {
		id   string
		code codes.Code
	}{{"", codes.OK}, {"native", codes.InvalidArgument}, {identity.PendingReference(), codes.Unavailable}, {"ocm_" + uuid.NewString(), codes.OK}} {
		creation := &platforms.CreationIdentity{ParentID: ref.id}
		encoded, err := marshalJSON(platforms.CreateSessionRequest{Directory: "/repo", Creation: creation})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.decodeSessionRequest(&pb.CallReq{Platform: "opencode", Payload: encoded}, &platforms.CreateSessionRequest{}); status.Code(err) != ref.code {
			t.Fatalf("creation parent %s: %v", ref.id, err)
		}
		if err := s.validateWorktreeIdentity("opencode", creation); status.Code(err) != ref.code {
			t.Fatalf("worktree parent %s: %v", ref.id, err)
		}
	}
	encoded, _ := marshalJSON(platforms.SendMessageRequest{SessionID: "ocm_" + uuid.NewString()})
	if err := s.decodeSessionRequest(&pb.CallReq{Platform: "r-other:opencode", Payload: encoded}, &platforms.SendMessageRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("owner RPC forwarded to a different owner: %v", err)
	}
	if err := s.decodeSessionRequest(&pb.CallReq{Platform: "opencode", Payload: []byte("invalid")}, &platforms.SendMessageRequest{}); err == nil {
		t.Fatal("malformed request accepted")
	}
	if err := s.decodeSessionRequest(&pb.CallReq{Platform: "opencode", Payload: encoded}, &struct{ SessionID string }{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("undeclared request contract accepted: %v", err)
	}
}
