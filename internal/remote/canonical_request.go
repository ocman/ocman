package remote

import (
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// decodeSessionRequest is the explicit v9 RPC request contract. Opaque prompt,
// tool, model, message and permission IDs are never searched or rewritten.
func (s *Server) decodeSessionRequest(req *pb.CallReq, dst any) error {
	if err := unmarshalJSON(req.Payload, dst); err != nil {
		return err
	}
	if s.registry == nil || !s.registry.CanonicalSessionIDs() {
		return nil
	}
	if _, err := s.platformFor(req.Platform); err != nil {
		return err
	}
	var id, parent string
	var required bool
	switch in := dst.(type) {
	case *platforms.SendMessageRequest:
		id, required = in.SessionID, true
	case *platforms.ExecuteCommandRequest:
		id, required = in.SessionID, true
	case *platforms.RunShellRequest:
		id, required = in.SessionID, true
	case *platforms.RespondPermissionRequest:
		id, required = in.SessionID, true
	case *platforms.RespondQuestionRequest:
		id, required = in.SessionID, true
	case *platforms.RejectQuestionRequest:
		id, required = in.SessionID, true
	case *platforms.CancelNativeQueuedRequest:
		id, required = in.SessionID, true
	case *platforms.AbortRequest:
		id, required = in.SessionID, true
	case *platforms.SetBackgroundLifecycleRequest:
		id, required = in.SessionID, true
	case *platforms.RenameSessionRequest:
		id, required = in.SessionID, true
	case *platforms.SetPermissionRulesRequest:
		id, required = in.SessionID, true
	case *platforms.CompactRequest:
		id, required = in.SessionID, true
	case *platforms.MoveSessionRequest:
		id, required = in.SessionID, true
	case *platforms.DisposeSessionRequest:
		id, required = in.SessionID, true
	case *platforms.ForkSessionRequest:
		id, required = in.SessionID, true
		if in.Creation != nil {
			parent = in.Creation.ParentID
		}
	case *platforms.CreateSessionRequest:
		if in.Creation != nil {
			parent = in.Creation.ParentID
		}
	case *platforms.DirectoryCatalogRequest:
	default:
		return status.Error(codes.InvalidArgument, "undeclared session RPC request contract")
	}
	return validateCanonicalReferences(id, parent, required)
}

func validateCanonicalReferences(id, parent string, required bool) error {
	for _, ref := range []string{id, parent} {
		if identity.IsPendingReference(ref) {
			return status.Error(codes.Unavailable, "session reference awaits its owning machine")
		}
	}
	if (required && !identity.IsSessionID(id)) || (parent != "" && !identity.IsSessionID(parent)) {
		return status.Error(codes.InvalidArgument, "canonical session ID required")
	}
	return nil
}

func (s *Server) validateWorktreeIdentity(platform string, creation *platforms.CreationIdentity) error {
	if s.registry == nil || !s.registry.CanonicalSessionIDs() {
		return nil
	}
	if _, err := s.platformFor(platform); err != nil {
		return err
	}
	var parent string
	if creation != nil {
		parent = creation.ParentID
	}
	return validateCanonicalReferences("", parent, false)
}
