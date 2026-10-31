package remote

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
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
	caps, _ := hostCall[hostsvc.HostCaps](context.Background(), h.conn, "HostCapabilities", nil)
	return caps
}

// hostCall sends args (when non-nil) as the payload of method and decodes
// the reply into T, failing closed with ErrRemoteOffline while disconnected.
func hostCall[T any](ctx context.Context, c *RemoteConn, method string, args any, opts ...grpc.CallOption) (T, error) {
	var out T
	payload, err := c.call(ctx, &pb.CallReq{Method: method}, args, opts...)
	if err != nil {
		return out, err
	}
	if err := unmarshalJSON(payload, &out); err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// hostExec is hostCall for operations with an empty reply.
func hostExec(ctx context.Context, c *RemoteConn, method string, args any) error {
	_, err := c.call(ctx, &pb.CallReq{Method: method}, args)
	return err
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
	out, err := hostCall[hostsvc.ProjectUpstreams](ctx, h.conn, "ProjectUpstreams", map[string]string{"dir": dir})
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
	}](ctx, h.conn, "FetchPRHead", req)
	return out.Branch, err
}

func (h *remoteHost) ListWorktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	out, err := hostCall[[]git.Worktree](ctx, h.conn, "ListWorktrees", map[string]any{"dir": dir})
	return out, notFoundAs(err, git.ErrNotARepo)
}

func (h *remoteHost) WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error) {
	out, err := hostCall[struct {
		BaseRef string `json:"baseRef"`
	}](ctx, h.conn, "WorktreeDefaultBaseRef", map[string]any{"dir": dir})
	return out.BaseRef, err
}

func (h *remoteHost) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	payload, err := h.conn.call(ctx, &pb.CallReq{Method: "CreateWorktreeSession"}, req)
	if err != nil {
		return nil, err
	}
	return h.decodeWorktreeSession(payload)
}

func (h *remoteHost) RemoveWorktree(ctx context.Context, req hostsvc.RemoveWorktreeRequest) error {
	return hostExec(ctx, h.conn, "RemoveWorktree", req)
}

func (h *remoteHost) LaunchTmux(ctx context.Context, req hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	return ptr(hostCall[hostsvc.LaunchTmuxResult](ctx, h.conn, "LaunchTmux", req))
}

func (h *remoteHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
	return ptr(hostCall[hostsvc.EnsureRuntimeResult](ctx, h.conn, "EnsureProjectOpencode", req))
}

func (h *remoteHost) EnsureRuntime(ctx context.Context, platform, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	out, err := hostCall[hostsvc.EnsureRuntimeResult](ctx, h.conn, "EnsureRuntime", map[string]string{"platform": platform, "projectDir": dir})
	if status.Code(err) == codes.Unimplemented && platform == string(platforms.OpenCodeID) {
		return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	}
	return ptr(out, err)
}

func (h *remoteHost) StopProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	return hostExec(ctx, h.conn, "StopProjectOpencode", req)
}

func (h *remoteHost) RestartProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
	return ptr(hostCall[hostsvc.EnsureRuntimeResult](ctx, h.conn, "RestartProjectOpencode", req))
}

func (h *remoteHost) ManagedOpencodes(ctx context.Context) ([]hostsvc.ManagedOpencode, error) {
	return hostCall[[]hostsvc.ManagedOpencode](ctx, h.conn, "ManagedOpencodes", nil)
}

func (h *remoteHost) TmuxSessions(ctx context.Context) ([]hostsvc.TmuxSession, error) {
	return hostCall[[]hostsvc.TmuxSession](ctx, h.conn, "TmuxSessions", nil)
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
			AssociatedDirectory: id.AssociatedDirectory,
			Directory:           id.Dir,
			SessionCount:        id.SessionCount,
			MessageCount:        id.MessageCount,
			LastUsed:            id.LastUsed,
			TotalTokensIn:       id.TotalTokensIn,
			TotalTokensOut:      id.TotalTokensOut,
			TotalCost:           id.TotalCost,
		})
	}
	return out, nil
}

func (h *remoteHost) TermWindows(ctx context.Context, dir string) ([]hostsvc.TermWindow, error) {
	return hostCall[[]hostsvc.TermWindow](ctx, h.conn, "TermWindows", map[string]any{"dir": dir})
}

func (h *remoteHost) TermCreateWindow(ctx context.Context, dir string) (string, error) {
	out, err := hostCall[struct {
		Window string `json:"window"`
	}](ctx, h.conn, "TermCreateWindow", map[string]any{"dir": dir})
	return out.Window, err
}

func (h *remoteHost) TermKillWindow(ctx context.Context, dir, window string) error {
	return hostExec(ctx, h.conn, "TermKillWindow", map[string]any{"dir": dir, "window": window})
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
	return hostCall[[]ProjectIdentity](ctx, h.conn, "Projects", nil)
}
