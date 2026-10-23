package remote

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// remoteHost implements hostsvc.Host by proxying each directory-scoped
// operation to the owning remote over the shared RemoteConn (AD-16). The
// remote executes its own local Host, so tmux panes / worktrees / child
// processes live on the owner (R-C).
type remoteHost struct {
	conn *RemoteConn
}

func newRemoteHost(conn *RemoteConn) *remoteHost { return &remoteHost{conn: conn} }

func (h *remoteHost) RemoteID() string { return h.conn.RemoteID() }

// Doctor is hub-local only for now; remotes report no checks.
func (h *remoteHost) Doctor(context.Context) []hostsvc.DoctorCheck { return nil }

func (h *remoteHost) Capabilities() hostsvc.HostCaps {
	caps, _ := hostQuery[hostsvc.HostCaps](context.Background(), h.conn, pb.OcmanClient.HostCapabilities)
	return caps
}

type (
	jsonRPC  func(pb.OcmanClient, context.Context, *pb.JsonReq, ...grpc.CallOption) (*pb.JsonResp, error)
	execRPC  func(pb.OcmanClient, context.Context, *pb.JsonReq, ...grpc.CallOption) (*pb.Empty, error)
	queryRPC func(pb.OcmanClient, context.Context, *pb.Empty, ...grpc.CallOption) (*pb.JsonResp, error)
)

// connCall runs one RPC on c, failing closed with ErrRemoteOffline while
// disconnected, and decodes the returned payload into T.
func connCall[T any](c *RemoteConn, call func(pb.OcmanClient) ([]byte, error)) (T, error) {
	var out T
	client := c.Client()
	if client == nil {
		return out, platforms.ErrRemoteOffline
	}
	payload, err := call(client)
	if err != nil {
		return out, err
	}
	if err := unmarshalJSON(payload, &out); err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// hostCall sends args as a JsonReq payload and decodes the JsonResp into T.
func hostCall[T any](ctx context.Context, c *RemoteConn, rpc jsonRPC, args any, opts ...grpc.CallOption) (T, error) {
	return connCall[T](c, func(client pb.OcmanClient) ([]byte, error) {
		b, err := marshalJSON(args)
		if err != nil {
			return nil, err
		}
		resp, err := rpc(client, ctx, &pb.JsonReq{Payload: b}, opts...)
		return resp.GetPayload(), err
	})
}

// hostExec is hostCall for RPCs with an empty reply.
func hostExec(ctx context.Context, c *RemoteConn, rpc execRPC, args any) error {
	_, err := connCall[struct{}](c, func(client pb.OcmanClient) ([]byte, error) {
		b, err := marshalJSON(args)
		if err != nil {
			return nil, err
		}
		_, err = rpc(client, ctx, &pb.JsonReq{Payload: b})
		return nil, err
	})
	return err
}

// hostQuery is hostCall for RPCs with an empty request.
func hostQuery[T any](ctx context.Context, c *RemoteConn, rpc queryRPC) (T, error) {
	return connCall[T](c, func(client pb.OcmanClient) ([]byte, error) {
		resp, err := rpc(client, ctx, &pb.Empty{})
		return resp.GetPayload(), err
	})
}

// ptr adapts a by-value result to the pointer the Host interface returns.
func ptr[T any](v T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// notFoundAs joins sentinel onto a codes.NotFound error so callers can
// errors.Is the owner's domain error across the wire.
func notFoundAs(err, sentinel error) error {
	if status.Code(err) == codes.NotFound {
		return errors.Join(sentinel, err)
	}
	return err
}

func (h *remoteHost) ProjectUpstreams(ctx context.Context, dir string) (*hostsvc.ProjectUpstreams, error) {
	out, err := hostCall[hostsvc.ProjectUpstreams](ctx, h.conn, pb.OcmanClient.ProjectUpstreams, map[string]string{"dir": dir})
	if err != nil {
		return nil, notFoundAs(err, git.ErrNotARepo)
	}
	for i := range out.Remotes {
		out.Remotes[i].URL = ""
	}
	return &out, nil
}

func (h *remoteHost) FetchPRHead(ctx context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
	out, err := hostCall[struct {
		Branch string `json:"branch"`
	}](ctx, h.conn, pb.OcmanClient.FetchPRHead, req)
	return out.Branch, err
}

func (h *remoteHost) ListWorktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	out, err := hostCall[[]git.Worktree](ctx, h.conn, pb.OcmanClient.ListWorktrees, map[string]any{"dir": dir})
	return out, notFoundAs(err, git.ErrNotARepo)
}

func (h *remoteHost) WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error) {
	out, err := hostCall[struct {
		BaseRef string `json:"baseRef"`
	}](ctx, h.conn, pb.OcmanClient.WorktreeDefaultBaseRef, map[string]any{"dir": dir})
	return out.BaseRef, err
}

func (h *remoteHost) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	raw, err := hostCall[json.RawMessage](ctx, h.conn, pb.OcmanClient.CreateWorktreeSession, req)
	if err != nil {
		return nil, err
	}
	return h.decodeWorktreeSession(raw)
}

func (h *remoteHost) RemoveWorktree(ctx context.Context, req hostsvc.RemoveWorktreeRequest) error {
	return hostExec(ctx, h.conn, pb.OcmanClient.RemoveWorktree, req)
}

func (h *remoteHost) LaunchTmux(ctx context.Context, req hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	return ptr(hostCall[hostsvc.LaunchTmuxResult](ctx, h.conn, pb.OcmanClient.LaunchTmux, req))
}

func (h *remoteHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
	return ptr(hostCall[hostsvc.EnsureRuntimeResult](ctx, h.conn, pb.OcmanClient.EnsureProjectOpencode, req))
}

func (h *remoteHost) EnsureRuntime(ctx context.Context, platform, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	out, err := hostCall[hostsvc.EnsureRuntimeResult](ctx, h.conn, pb.OcmanClient.EnsureRuntime, map[string]string{"platform": platform, "projectDir": dir})
	if status.Code(err) == codes.Unimplemented && platform == string(platforms.OpenCodeID) {
		return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	}
	return ptr(out, err)
}

func (h *remoteHost) StopProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	return hostExec(ctx, h.conn, pb.OcmanClient.StopProjectOpencode, req)
}

func (h *remoteHost) RestartProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
	return ptr(hostCall[hostsvc.EnsureRuntimeResult](ctx, h.conn, pb.OcmanClient.RestartProjectOpencode, req))
}

func (h *remoteHost) ManagedOpencodes(ctx context.Context) ([]hostsvc.ManagedOpencode, error) {
	return hostQuery[[]hostsvc.ManagedOpencode](ctx, h.conn, pb.OcmanClient.ManagedOpencodes)
}

func (h *remoteHost) TmuxSessions(ctx context.Context) ([]hostsvc.TmuxSession, error) {
	return hostQuery[[]hostsvc.TmuxSession](ctx, h.conn, pb.OcmanClient.TmuxSessions)
}

// Projects maps the remote's ProjectIdentity records (identity + aggregate
// stats) onto the ProjectStats the Host interface expects.
func (h *remoteHost) Projects(ctx context.Context) ([]db.ProjectStats, error) {
	idents, err := h.ProjectIdentities(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.ProjectStats, 0, len(idents))
	for _, id := range idents {
		out = append(out, db.ProjectStats{
			Directory:      id.Dir,
			SessionCount:   id.SessionCount,
			MessageCount:   id.MessageCount,
			LastUsed:       id.LastUsed,
			TotalTokensIn:  id.TotalTokensIn,
			TotalTokensOut: id.TotalTokensOut,
			TotalCost:      id.TotalCost,
		})
	}
	return out, nil
}

func (h *remoteHost) TermWindows(ctx context.Context, dir string) ([]hostsvc.TermWindow, error) {
	return hostCall[[]hostsvc.TermWindow](ctx, h.conn, pb.OcmanClient.TermWindows, map[string]any{"dir": dir})
}

func (h *remoteHost) TermCreateWindow(ctx context.Context, dir string) (string, error) {
	out, err := hostCall[struct {
		Window string `json:"window"`
	}](ctx, h.conn, pb.OcmanClient.TermCreateWindow, map[string]any{"dir": dir})
	return out.Window, err
}

func (h *remoteHost) TermKillWindow(ctx context.Context, dir, window string) error {
	return hostExec(ctx, h.conn, pb.OcmanClient.TermKillWindow, map[string]any{"dir": dir, "window": window})
}

// TermAttach tunnels a browser terminal to the remote's PTY over the
// bidi TerminalStream RPC: the first frame carries the window selection
// (open), then viewer keystrokes/resizes are forwarded and PTY output is
// written back to conn. Runs the shell on the remote machine (R-C).
func (h *remoteHost) TermAttach(ctx context.Context, req hostsvc.TermAttachRequest, conn hostsvc.TermConn) error {
	client := h.conn.Client()
	if client == nil {
		return platforms.ErrRemoteOffline
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.TerminalStream(ctx)
	if err != nil {
		return err
	}
	// First frame selects the target window on the remote.
	if err := stream.Send(&pb.TermClientMsg{Open: &pb.TermOpen{
		Dir:      req.Dir,
		Window:   req.Window,
		Readonly: req.Readonly,
	}}); err != nil {
		return err
	}

	// gRPC -> conn: remote PTY output back to the viewer.
	go func() {
		defer cancel()
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				return
			}
			if len(msg.Data) > 0 {
				if werr := conn.Write(msg.Data); werr != nil {
					return
				}
			}
		}
	}()

	// conn -> gRPC: viewer keystrokes/resizes to the remote PTY.
	for {
		frame, recvErr := conn.Recv()
		if recvErr != nil {
			_ = stream.CloseSend()
			return nil
		}
		var out pb.TermClientMsg
		switch {
		case frame.Resize != nil:
			out.Resize = &pb.TermResize{Cols: uint32(frame.Resize.Cols), Rows: uint32(frame.Resize.Rows)}
		case len(frame.Data) > 0:
			out.Data = frame.Data
		default:
			continue
		}
		if err := stream.Send(&out); err != nil {
			return nil
		}
	}
}

// ProjectIdentities fetches the remote's project inventory directly (used
// by the Manager's inventory cache, Phase 8).
func (h *remoteHost) ProjectIdentities(ctx context.Context) ([]ProjectIdentity, error) {
	return hostQuery[[]ProjectIdentity](ctx, h.conn, pb.OcmanClient.Projects)
}
