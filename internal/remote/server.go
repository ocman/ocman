package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/webhook"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server translates authenticated owner-local RPCs into platform and host
// operations. Mutations use the same sessionsvc service as REST handlers.
type Server struct {
	pb.UnimplementedOcmanServer
	registry          *platforms.Registry
	sessions          *sessionsvc.Service
	host              hostsvc.Host
	inboxStore        *state.DB
	instanceID        string
	version           string
	enrichSession     func(context.Context, string, string, *platforms.SessionDetail)
	proxyEvents       func(context.Context, string, string, platforms.Platform, io.Writer, io.Writer, func()) error
	webhookDispatcher webhook.RoutineDispatcher
	plugins           PluginHandler
}

func (s *Server) UseInboxStore(store *state.DB) *Server { s.inboxStore = store; return s }
func (s *Server) UseWebhookDispatcher(dispatcher webhook.RoutineDispatcher) *Server {
	s.webhookDispatcher = dispatcher
	return s
}
func NewServer(registry *platforms.Registry, host hostsvc.Host, instanceID, version string) *Server {
	return &Server{registry: registry, sessions: sessionsvc.New(registry, sessionsvc.Hooks{}), host: host, instanceID: instanceID, version: version}
}
func (s *Server) UseSessions(svc *sessionsvc.Service) *Server { s.sessions = svc; return s }
func (s *Server) UseSessionEnricher(fn func(context.Context, string, string, *platforms.SessionDetail)) *Server {
	s.enrichSession = fn
	return s
}
func (s *Server) UseEventProxy(fn func(context.Context, string, string, platforms.Platform, io.Writer, io.Writer, func()) error) *Server {
	s.proxyEvents = fn
	return s
}
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
	if errors.Is(err, platforms.ErrUnsupported) {
		return status.Error(codes.Unimplemented, err.Error())
	}
	return err
}
func (s *Server) platformFor(id string) (platforms.Platform, error) {
	p, ok := s.registry.Get(platforms.ID(id))
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown platform %q", id)
	}
	return p, nil
}
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
func (s *Server) Hello(_ context.Context, _ *pb.HelloReq) (*pb.HelloResp, error) {
	hostname, _ := os.Hostname()
	return &pb.HelloResp{ProtocolVersion: ProtocolVersion, InstanceId: s.instanceID, Hostname: hostname, OcmanVersion: s.version}, nil
}

func (s *Server) StreamEvents(req *pb.SessionRef, stream pb.Ocman_StreamEventsServer) error {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return err
	}
	w := &eventStreamWriter{stream: stream}
	raw := &sseFrameWriter{dst: w}
	if s.proxyEvents != nil {
		return s.proxyEvents(stream.Context(), req.Platform, req.SessionId, p, raw, w, func() {})
	}
	return p.ProxyEvents(stream.Context(), req.SessionId, raw, func() {})
}

type eventStreamWriter struct {
	stream pb.Ocman_StreamEventsServer
	mu     sync.Mutex
}

func (w *eventStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The upstream buffer may be reused after Write returns.
	chunk := make([]byte, len(p))
	copy(chunk, p)
	if err := w.stream.Send(&pb.EventChunk{Data: chunk}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Hold partial upstream writes until a complete SSE frame is available.
type sseFrameWriter struct {
	dst     io.Writer
	mu      sync.Mutex
	pending []byte
}

const maxSSEFrameBytes = 4 << 20

func (w *sseFrameWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	if len(w.pending) > maxSSEFrameBytes && sseFrameEnd(w.pending) < 0 {
		w.pending = nil
		return 0, fmt.Errorf("remote: SSE frame exceeds %d bytes", maxSSEFrameBytes)
	}
	for {
		end := sseFrameEnd(w.pending)
		if end < 0 {
			return len(p), nil
		}
		if _, err := w.dst.Write(w.pending[:end]); err != nil {
			return len(p), err
		}
		w.pending = w.pending[end:]
		if len(w.pending) == 0 {
			w.pending = nil
		}
	}
}
func sseFrameEnd(p []byte) int {
	lf, crlf := bytes.Index(p, []byte("\n\n")), bytes.Index(p, []byte("\n\r\n"))
	if lf >= 0 && (crlf < 0 || lf < crlf) {
		return lf + 2
	}
	if crlf >= 0 {
		return crlf + 3
	}
	return -1
}

func (s *Server) RegisterWebhookInbox(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "webhook state is unavailable")
	}
	var input struct {
		RoutineID       string `json:"routineId"`
		RelayURL        string `json:"relayUrl"`
		EnrollmentToken string `json:"enrollmentToken"`
		Secret          string `json:"secret"`
		SecretHeader    string `json:"secretHeader"`
	}
	if err := unmarshalJSON(req.Payload, &input); err != nil {
		return nil, err
	}
	inbox, err := webhook.RegisterWithSecret(ctx, s.inboxStore, input.RoutineID, input.RelayURL, input.EnrollmentToken, input.Secret, input.SecretHeader, nil)
	return jsonResp(inbox, err)
}
func (s *Server) PollWebhookInbox(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "webhook state is unavailable")
	}
	var input struct {
		RoutineID string `json:"routineId"`
	}
	if err := unmarshalJSON(req.Payload, &input); err != nil {
		return nil, err
	}
	inbox, err := s.inboxStore.GetWebhookInbox(ctx, input.RoutineID)
	if err != nil {
		return nil, err
	}
	return &pb.Empty{}, (&webhook.Poller{Store: s.inboxStore, Inbox: inbox, Routines: s.webhookDispatcher}).Poll(ctx)
}

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
func (s *Server) TerminalStream(stream pb.Ocman_TerminalStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.Open == nil {
		return status.Error(codes.InvalidArgument, "first terminal message must carry open")
	}
	conn := &streamTermConn{stream: stream}
	return s.host.TermAttach(stream.Context(), hostsvc.TermAttachRequest{Dir: first.Open.Dir, Window: first.Open.Window, Readonly: first.Open.Readonly}, conn)
}

type streamTermConn struct{ stream pb.Ocman_TerminalStreamServer }

func (c *streamTermConn) Recv() (hostsvc.TermFrame, error) {
	for {
		msg, err := c.stream.Recv()
		if err != nil {
			return hostsvc.TermFrame{}, err
		}
		if msg.Resize != nil {
			return hostsvc.TermFrame{Resize: &hostsvc.TermSize{Cols: uint16(msg.Resize.Cols), Rows: uint16(msg.Resize.Rows)}}, nil
		}
		if len(msg.Data) > 0 {
			return hostsvc.TermFrame{Data: msg.Data}, nil
		}
	}
}
func (c *streamTermConn) Write(p []byte) error {
	chunk := make([]byte, len(p))
	copy(chunk, p)
	return c.stream.Send(&pb.TermServerMsg{Data: chunk})
}
func (c *streamTermConn) Close() error { return nil }
func (s *Server) Projects(ctx context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	projects, err := s.host.Projects(ctx)
	if err != nil {
		return nil, err
	}
	return jsonResp(projectIdentities(projects), nil)
}
