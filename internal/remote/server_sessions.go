package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) Sessions(ctx context.Context, req *pb.SessionsReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.Sessions(ctx, req.Dir, req.Since))
}
func (s *Server) Session(ctx context.Context, req *pb.SessionReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	detail, err := p.Session(ctx, req.SessionId, int(req.Limit), int(req.Offset))
	if err == nil && detail != nil && s.enrichSession != nil {
		s.enrichSession(ctx, req.Platform, req.SessionId, detail)
	}
	return jsonResp(detail, err)
}
func (s *Server) SessionsInactiveBefore(ctx context.Context, req *pb.CutoffReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SessionsInactiveBefore(ctx, req.Cutoff))
}
func (s *Server) SessionChanges(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SessionChanges(ctx, req.SessionId))
}
func (s *Server) SessionInfo(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	info, err := p.SessionInfo(ctx, req.SessionId)
	if err != nil || info == nil || s.inboxStore == nil {
		return jsonResp(info, err)
	}
	commits, err := s.inboxStore.ListSessionCommits(ctx, req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	info.CommitCaptureSupported = true
	info.Commits = make([]platforms.SessionCommit, 0, len(commits))
	for _, commit := range commits {
		info.Commits = append(info.Commits, platforms.SessionCommit{
			Order: commit.Order, SHA: commit.SHA, Branch: commit.Branch, Subject: commit.Subject,
			SourceMessageID: commit.SourceMessageID, ToolPartID: commit.ToolPartID,
			ToolCallID: commit.ToolCallID, ObservedAt: commit.ObservedAt,
		})
	}
	return jsonResp(info, nil)
}
func (s *Server) AgentCatalog(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.AgentCatalog(ctx, req.SessionId))
}
func (s *Server) SlashCommands(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SlashCommands(ctx, req.SessionId))
}
func (s *Server) SessionModels(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SessionModels(ctx, req.SessionId))
}
func (s *Server) DirectoryCatalog(ctx context.Context, req *pb.PlatformJsonReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	var in platforms.DirectoryCatalogRequest
	if err := unmarshalJSON(req.Payload, &in); err != nil {
		return nil, err
	}
	return jsonResp(p.DirectoryCatalog(ctx, in))
}
func (s *Server) ListPermissions(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.ListPermissions(ctx, req.SessionId))
}

// An unsupported authoritative read must fail rather than use observed cache.
func (s *Server) RefreshPermissions(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	live, ok := p.(platforms.PermissionRefresher)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "platform has no authoritative permission list")
	}
	return jsonResp(live.RefreshPermissions(ctx, req.SessionId))
}
func (s *Server) ListQuestions(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.ListQuestions(ctx, req.SessionId))
}
func (s *Server) Capabilities(_ context.Context, req *pb.PlatformRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.Capabilities(), nil)
}
func (s *Server) Owns(ctx context.Context, req *pb.SessionRef) (*pb.OwnsResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return &pb.OwnsResp{Owns: p.Owns(ctx, req.SessionId)}, nil
}
func (s *Server) SendMessage(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var mr platforms.SendMessageRequest
	if err := unmarshalJSON(req.Payload, &mr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.SendMessage(ctx, req.Platform, mr))
}
func (s *Server) ExecuteCommand(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var cr platforms.ExecuteCommandRequest
	if err := unmarshalJSON(req.Payload, &cr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.ExecuteCommand(ctx, req.Platform, cr))
}
func (s *Server) RunShell(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var sr platforms.RunShellRequest
	if err := unmarshalJSON(req.Payload, &sr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.RunShell(ctx, req.Platform, sr))
}
func (s *Server) RespondPermission(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var rr platforms.RespondPermissionRequest
	if err := unmarshalJSON(req.Payload, &rr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.RespondPermission(ctx, req.Platform, rr))
}
func (s *Server) RespondQuestion(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var rr platforms.RespondQuestionRequest
	if err := unmarshalJSON(req.Payload, &rr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.RespondQuestion(ctx, req.Platform, rr))
}
func (s *Server) RejectQuestion(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var rr platforms.RejectQuestionRequest
	if err := unmarshalJSON(req.Payload, &rr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.RejectQuestion(ctx, req.Platform, rr))
}
func (s *Server) NativeQueued(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	nq, ok := p.(platforms.NativeQueue)
	if !ok {
		return nil, svcErr(platforms.ErrUnsupported)
	}
	msgs, err := nq.NativeQueued(ctx, req.SessionId)
	if err != nil {
		return nil, svcErr(err)
	}
	return jsonResp(msgs, nil)
}
func (s *Server) CancelNativeQueued(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var cr platforms.CancelNativeQueuedRequest
	if err := unmarshalJSON(req.Payload, &cr); err != nil {
		return nil, err
	}
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	nq, ok := p.(platforms.NativeQueue)
	if !ok {
		return nil, svcErr(platforms.ErrUnsupported)
	}
	return &pb.Empty{}, svcErr(nq.CancelNativeQueued(ctx, cr))
}
func (s *Server) Abort(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var ar platforms.AbortRequest
	if err := unmarshalJSON(req.Payload, &ar); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.Abort(ctx, req.Platform, ar))
}
func (s *Server) RenameSession(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var rr platforms.RenameSessionRequest
	if err := unmarshalJSON(req.Payload, &rr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.Rename(ctx, req.Platform, rr))
}
func (s *Server) PermissionRules(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.PermissionRules(ctx, req.SessionId))
}
func (s *Server) SetPermissionRules(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var sr platforms.SetPermissionRulesRequest
	if err := unmarshalJSON(req.Payload, &sr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.SetPermissionRules(ctx, req.Platform, sr))
}
func (s *Server) Compact(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var cr platforms.CompactRequest
	if err := unmarshalJSON(req.Payload, &cr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.Compact(ctx, req.Platform, cr))
}
func (s *Server) ForkSession(ctx context.Context, req *pb.PlatformJsonReq) (*pb.JsonResp, error) {
	var fr platforms.ForkSessionRequest
	if err := unmarshalJSON(req.Payload, &fr); err != nil {
		return nil, err
	}
	resp, err := s.sessions.Fork(ctx, req.Platform, fr)
	return jsonResp(resp, svcErr(err))
}
func (s *Server) MoveSession(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var mr platforms.MoveSessionRequest
	if err := unmarshalJSON(req.Payload, &mr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.Move(ctx, req.Platform, mr))
}
func (s *Server) CreateSession(ctx context.Context, req *pb.PlatformJsonReq) (*pb.JsonResp, error) {
	var cr platforms.CreateSessionRequest
	if err := unmarshalJSON(req.Payload, &cr); err != nil {
		return nil, err
	}
	log.WithFields(log.Fields{"platform": req.Platform, "directory": cr.Directory}).Info("remote: create session request from hub")
	resp, err := s.sessions.Create(ctx, req.Platform, cr)
	if err != nil {
		log.WithError(err).WithField("directory", cr.Directory).Warn("remote: create session failed")
	}
	return jsonResp(resp, svcErr(err))
}
