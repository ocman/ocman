package remote

import (
	"context"
	"errors"
	"os"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

// Server is the remote-side gRPC service. It is a thin translation layer
// over the local platforms.Registry and hostsvc.Host: reads resolve the
// local adapter from the base platform id (or the local Host) and call
// the matching method, marshalling rich results as JSON (AD-3, AD-11).
// Session mutations delegate to the shared sessionsvc.Service so they
// take the same validated code path as the REST handlers and MCP tools.
type Server struct {
	pb.UnimplementedOcmanServer

	registry   *platforms.Registry
	sessions   *sessionsvc.Service
	host       hostsvc.Host
	instanceID string
	version    string
	origins    *originCache
}

// NewServer builds the remote-side gRPC service over the given local
// registry and host. instanceID is this ocman's stable random ID;
// version is the ocman build version reported in Hello.
func NewServer(registry *platforms.Registry, host hostsvc.Host, instanceID, version string) *Server {
	return &Server{
		registry:   registry,
		sessions:   sessionsvc.New(registry, sessionsvc.Hooks{}),
		host:       host,
		instanceID: instanceID,
		version:    version,
		origins:    newOriginCache(),
	}
}

// UseSessions swaps in a shared session service. main.go passes the
// HTTP server's, so host-local hooks (auto-approve judge cancellation,
// projects-index refresh) also fire for gRPC-executed mutations.
func (s *Server) UseSessions(svc *sessionsvc.Service) *Server {
	s.sessions = svc
	return s
}

// svcErr maps sessionsvc errors to gRPC status codes.
func svcErr(err error) error {
	if err == nil {
		return nil
	}
	var ve *sessionsvc.ValidationError
	if errors.As(err, &ve) {
		return status.Error(codes.InvalidArgument, ve.Error())
	}
	if errors.Is(err, platforms.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, platforms.ErrPlatformUnreachable) {
		return status.Error(codes.Unavailable, err.Error())
	}
	return err
}

// platformFor resolves the local adapter for a base platform id.
func (s *Server) platformFor(id string) (platforms.Platform, error) {
	p, ok := s.registry.Get(platforms.ID(id))
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown platform %q", id)
	}
	return p, nil
}

// jsonResp wraps a value into a *pb.JsonResp, or an error.
func jsonResp(v any, err error) (*pb.JsonResp, error) {
	if err != nil {
		if errors.Is(err, platforms.ErrPlatformUnreachable) {
			return nil, status.Error(codes.Unavailable, err.Error())
		}
		return nil, err
	}
	b, err := marshalJSON(v)
	if err != nil {
		return nil, err
	}
	return &pb.JsonResp{Payload: b}, nil
}

// --- Hello ---

func (s *Server) Hello(_ context.Context, _ *pb.HelloReq) (*pb.HelloResp, error) {
	hostname, _ := os.Hostname()
	return &pb.HelloResp{
		ProtocolVersion: ProtocolVersion,
		InstanceId:      s.instanceID,
		Hostname:        hostname,
		OcmanVersion:    s.version,
	}, nil
}

// --- Session reads ---

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
	return jsonResp(p.Session(ctx, req.SessionId, int(req.Limit), int(req.Offset)))
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
	return jsonResp(p.SessionInfo(ctx, req.SessionId))
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

func (s *Server) ListPermissions(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	return jsonResp(p.ListPermissions(ctx, req.SessionId))
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

// --- Session mutations ---

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
	log.WithFields(log.Fields{
		"platform":  req.Platform,
		"directory": cr.Directory,
	}).Info("remote: create session request from hub")
	resp, err := s.sessions.Create(ctx, req.Platform, cr)
	if err != nil {
		log.WithError(err).WithField("directory", cr.Directory).Warn("remote: create session failed")
	}
	return jsonResp(resp, svcErr(err))
}

// --- Streaming events ---

func (s *Server) StreamEvents(req *pb.SessionRef, stream pb.Ocman_StreamEventsServer) error {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return err
	}
	w := &eventStreamWriter{stream: stream}
	return p.ProxyEvents(stream.Context(), req.SessionId, w, func() {})
}

// eventStreamWriter adapts a gRPC server-stream to the io.Writer+flush
// shape ProxyEvents expects: each Write frames the bytes into an
// EventChunk message tunneled to the hub (AD-14).
type eventStreamWriter struct {
	stream pb.Ocman_StreamEventsServer
}

func (w *eventStreamWriter) Write(p []byte) (int, error) {
	// Copy because the underlying SSE buffer may be reused after Write
	// returns; the gRPC send is asynchronous w.r.t. the caller's buffer.
	chunk := make([]byte, len(p))
	copy(chunk, p)
	if err := w.stream.Send(&pb.EventChunk{Data: chunk}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// --- Host services ---

func (s *Server) GitInfo(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var dirs []string
	if err := unmarshalJSON(req.Payload, &dirs); err != nil {
		return nil, err
	}
	return jsonResp(s.host.GitInfo(ctx, dirs))
}

func (s *Server) GitDiff(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir   string `json:"dir"`
		Force bool   `json:"force"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(s.host.GitDiff(ctx, args.Dir, hostsvc.GitDiffOptions{Force: args.Force}))
}

func (s *Server) GitBranches(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(s.host.GitBranches(ctx, args.Dir))
}

func (s *Server) GitCheckout(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	var args struct {
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	// ponytail: git.ErrDirtyCheckout does not survive gRPC as a typed
	// sentinel; the message string is preserved and the HTTP handler
	// re-matches it. Upgrade to a status code detail if callers need it.
	return &pb.Empty{}, s.host.GitCheckout(ctx, args.Dir, args.Branch)
}

func (s *Server) ListWorktrees(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(s.host.ListWorktrees(ctx, args.Dir))
}

func (s *Server) WorktreeDefaultBaseRef(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	ref, err := s.host.WorktreeDefaultBaseRef(ctx, args.Dir)
	return jsonResp(map[string]string{"baseRef": ref}, err)
}

func (s *Server) CreateWorktreeSession(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var wr hostsvc.WorktreeSessionRequest
	if err := unmarshalJSON(req.Payload, &wr); err != nil {
		return nil, err
	}
	return jsonResp(s.host.CreateWorktreeSession(ctx, wr))
}

func (s *Server) RemoveWorktree(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	var wr hostsvc.RemoveWorktreeRequest
	if err := unmarshalJSON(req.Payload, &wr); err != nil {
		return nil, err
	}
	return &pb.Empty{}, s.host.RemoveWorktree(ctx, wr)
}

func (s *Server) LaunchTmux(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var lr hostsvc.LaunchTmuxRequest
	if err := unmarshalJSON(req.Payload, &lr); err != nil {
		return nil, err
	}
	log.WithField("directory", lr.Directory).Info("remote: launch-tmux request from hub")
	return jsonResp(s.host.LaunchTmux(ctx, lr))
}

func (s *Server) EnsureProjectOpencode(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var er hostsvc.EnsureProjectOpencodeRequest
	if err := unmarshalJSON(req.Payload, &er); err != nil {
		return nil, err
	}
	log.WithField("projectDir", er.ProjectDir).Info("remote: ensure-project-opencode request from hub")
	return jsonResp(s.host.EnsureProjectOpencode(ctx, er))
}

func (s *Server) StopProjectOpencode(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	var er hostsvc.EnsureProjectOpencodeRequest
	if err := unmarshalJSON(req.Payload, &er); err != nil {
		return nil, err
	}
	log.WithField("projectDir", er.ProjectDir).Info("remote: stop-project-opencode request from hub")
	return &pb.Empty{}, s.host.StopProjectOpencode(ctx, er)
}

func (s *Server) RestartProjectOpencode(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var er hostsvc.EnsureProjectOpencodeRequest
	if err := unmarshalJSON(req.Payload, &er); err != nil {
		return nil, err
	}
	log.WithField("projectDir", er.ProjectDir).Info("remote: restart-project-opencode request from hub")
	return jsonResp(s.host.RestartProjectOpencode(ctx, er))
}

func (s *Server) ManagedOpencodes(ctx context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	return jsonResp(s.host.ManagedOpencodes(ctx))
}

func (s *Server) TmuxSessions(ctx context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	return jsonResp(s.host.TmuxSessions(ctx))
}

func (s *Server) HostCapabilities(_ context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	return jsonResp(s.host.Capabilities(), nil)
}

func (s *Server) BeadsStatus(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(s.host.BeadsStatus(ctx, args.Dir))
}

func (s *Server) DaguStatus(ctx context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	return jsonResp(s.host.DaguStatus(ctx), nil)
}

// --- Terminal ---

func (s *Server) TermWindows(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	wins, err := s.host.TermWindows(ctx, args.Dir)
	if err != nil {
		return nil, err
	}
	if wins == nil {
		wins = []hostsvc.TermWindow{}
	}
	return jsonResp(wins, nil)
}

func (s *Server) TermCreateWindow(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	name, err := s.host.TermCreateWindow(ctx, args.Dir)
	return jsonResp(map[string]string{"window": name}, err)
}

func (s *Server) TermKillWindow(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	var args struct {
		Dir    string `json:"dir"`
		Window string `json:"window"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return &pb.Empty{}, s.host.TermKillWindow(ctx, args.Dir, args.Window)
}

// TerminalStream bridges the hub's TerminalStream to the remote's local
// PTY: the first client message selects the window, then the local
// Host's TermAttach runs the PTY loop against a streamTermConn that
// tunnels frames over this gRPC stream. The shell runs on this machine.
func (s *Server) TerminalStream(stream pb.Ocman_TerminalStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.Open == nil {
		return status.Error(codes.InvalidArgument, "first terminal message must carry open")
	}
	conn := &streamTermConn{stream: stream}
	return s.host.TermAttach(stream.Context(), hostsvc.TermAttachRequest{
		Dir:      first.Open.Dir,
		Window:   first.Open.Window,
		Readonly: first.Open.Readonly,
	}, conn)
}

// streamTermConn adapts the remote-side gRPC stream to hostsvc.TermConn
// so the remote's local Host drives its PTY as if the browser were
// attached directly.
type streamTermConn struct {
	stream pb.Ocman_TerminalStreamServer
}

func (c *streamTermConn) Recv() (hostsvc.TermFrame, error) {
	for {
		msg, err := c.stream.Recv()
		if err != nil {
			return hostsvc.TermFrame{}, err
		}
		if msg.Resize != nil {
			return hostsvc.TermFrame{Resize: &hostsvc.TermSize{
				Cols: uint16(msg.Resize.Cols), Rows: uint16(msg.Resize.Rows),
			}}, nil
		}
		if len(msg.Data) > 0 {
			return hostsvc.TermFrame{Data: msg.Data}, nil
		}
		// Ignore empty/open-only frames after the first.
	}
}

func (c *streamTermConn) Write(p []byte) error {
	chunk := make([]byte, len(p))
	copy(chunk, p)
	return c.stream.Send(&pb.TermServerMsg{Data: chunk})
}

func (c *streamTermConn) Close() error { return nil }

// --- Project inventory ---

func (s *Server) Projects(ctx context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	projects, err := s.host.Projects(ctx)
	if err != nil {
		return nil, err
	}
	return jsonResp(projectIdentities(ctx, s.origins, projects), nil)
}
