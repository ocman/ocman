package remote

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) Sessions(ctx context.Context, req *pb.SessionsReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	if len(req.SidebarQuery) > 0 {
		var query platforms.SidebarQuery
		if err := json.Unmarshal(req.SidebarQuery, &query); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid sidebar query")
		}
		reader, ok := p.(platforms.SidebarReader)
		if !ok {
			return nil, status.Error(codes.Unimplemented, "compact sidebar unavailable")
		}
		rows, err := reader.SidebarSessions(ctx, query)
		return jsonResp(rows, err)
	}
	rows, err := p.Sessions(ctx, req.Dir, req.Since)
	if err != nil || s.inboxStore == nil {
		return jsonResp(rows, err)
	}
	if err := s.sessions.WaitForRoutineCreation(ctx); err != nil {
		return nil, err
	}
	routines, err := s.inboxStore.RoutineSessions(ctx)
	if err != nil {
		return nil, err
	}
	rows = append([]db.Session(nil), rows...)
	for i := range rows {
		rows[i].RoutineID = routines[state.Key{Platform: req.Platform, SessionID: rows[i].ID}]
		if rows[i].RoutineID == "" && rows[i].ParentID != "" {
			rows[i].RoutineID = routines[state.Key{Platform: req.Platform, SessionID: rows[i].ParentID}]
		}
	}
	return jsonResp(rows, nil)
}
func (s *Server) Session(ctx context.Context, req *pb.SessionReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	// Owners return stored history uncomposed. The hub serving the browser
	// composes it once with the stream it relays (convstream.Hub.BeginRead).
	detail, err := p.Session(ctx, req.SessionId, int(req.Limit), int(req.Offset))
	if err == nil && detail != nil && s.enrichSession != nil {
		s.enrichSession(ctx, req.Platform, req.SessionId, detail)
	}
	return jsonResp(detail, svcErr(err))
}
func (s *Server) SessionsInactiveBefore(ctx context.Context, req *pb.CutoffReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SessionsInactiveBefore(ctx, req.Cutoff))
}
func (s *Server) SessionChanges(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SessionChanges(ctx, req.SessionId))
}
func (s *Server) SessionInfo(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
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
func (s *Server) AgentCatalog(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.AgentCatalog(ctx, req.SessionId))
}
func (s *Server) SlashCommands(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SlashCommands(ctx, req.SessionId))
}
func (s *Server) SessionModels(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.SessionModels(ctx, req.SessionId))
}
func (s *Server) DirectoryCatalog(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	var in platforms.DirectoryCatalogRequest
	if err := s.decodeSessionRequest(req, &in); err != nil {
		return nil, err
	}
	return jsonResp(p.DirectoryCatalog(ctx, in))
}
func (s *Server) ListPermissions(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(s.permissionNotifications(ctx, p, req.SessionId))
}

// An unsupported authoritative read must fail rather than use observed cache.
func (s *Server) RefreshPermissions(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	live, ok := p.(platforms.PermissionRefresher)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "platform has no authoritative permission list")
	}
	return jsonResp(live.RefreshPermissions(ctx, req.SessionId))
}
func (s *Server) ListQuestions(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.ListQuestions(ctx, req.SessionId))
}
func (s *Server) Capabilities(_ context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.Capabilities(), nil)
}
func (s *Server) SendMessage(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var mr platforms.SendMessageRequest
	if err := s.decodeSessionRequest(req, &mr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.SendMessage(ctx, req.Platform, mr))
}
func (s *Server) ExecuteCommand(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var cr platforms.ExecuteCommandRequest
	if err := s.decodeSessionRequest(req, &cr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.ExecuteCommand(ctx, req.Platform, cr))
}
func (s *Server) RunShell(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var sr platforms.RunShellRequest
	if err := s.decodeSessionRequest(req, &sr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.RunShell(ctx, req.Platform, sr))
}
func (s *Server) RespondPermission(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var rr platforms.RespondPermissionRequest
	if err := s.decodeSessionRequest(req, &rr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.RespondPermission(ctx, req.Platform, rr))
}
func (s *Server) RespondQuestion(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var rr platforms.RespondQuestionRequest
	if err := s.decodeSessionRequest(req, &rr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.RespondQuestion(ctx, req.Platform, rr))
}
func (s *Server) RejectQuestion(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var rr platforms.RejectQuestionRequest
	if err := s.decodeSessionRequest(req, &rr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.RejectQuestion(ctx, req.Platform, rr))
}
func (s *Server) NativeQueued(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
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
func (s *Server) CancelNativeQueued(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var cr platforms.CancelNativeQueuedRequest
	if err := s.decodeSessionRequest(req, &cr); err != nil {
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
	return &pb.JsonResp{}, svcErr(nq.CancelNativeQueued(ctx, cr))
}
func (s *Server) Abort(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var ar platforms.AbortRequest
	if err := s.decodeSessionRequest(req, &ar); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.Abort(ctx, req.Platform, ar))
}
func (s *Server) RenameSession(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var rr platforms.RenameSessionRequest
	if err := s.decodeSessionRequest(req, &rr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.Rename(ctx, req.Platform, rr))
}
func (s *Server) PermissionRules(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.PermissionRules(ctx, req.SessionId))
}
func (s *Server) SetPermissionRules(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var sr platforms.SetPermissionRulesRequest
	if err := s.decodeSessionRequest(req, &sr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.SetPermissionRules(ctx, req.Platform, sr))
}
func (s *Server) Compact(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var cr platforms.CompactRequest
	if err := s.decodeSessionRequest(req, &cr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.Compact(ctx, req.Platform, cr))
}
func (s *Server) ForkSession(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var fr platforms.ForkSessionRequest
	if err := s.decodeSessionRequest(req, &fr); err != nil {
		return nil, err
	}
	resp, err := s.sessions.Fork(ctx, req.Platform, fr)
	return jsonResp(resp, svcErr(err))
}
func (s *Server) MoveSession(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var mr platforms.MoveSessionRequest
	if err := s.decodeSessionRequest(req, &mr); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.Move(ctx, req.Platform, mr))
}
func (s *Server) CreateSession(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var cr platforms.CreateSessionRequest
	if err := s.decodeSessionRequest(req, &cr); err != nil {
		return nil, err
	}
	log.WithFields(log.Fields{"platform": req.Platform, "directory": cr.Directory}).Info("remote: create session request from hub")
	resp, err := s.sessions.Create(ctx, req.Platform, cr)
	if err != nil {
		log.WithError(err).WithField("directory", cr.Directory).Warn("remote: create session failed")
	}
	return jsonResp(resp, svcErr(err))
}
