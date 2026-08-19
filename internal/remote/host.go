package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
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

func (h *remoteHost) Capabilities() hostsvc.HostCaps {
	client := h.conn.Client()
	if client == nil {
		return hostsvc.HostCaps{}
	}
	resp, err := client.HostCapabilities(context.Background(), &pb.Empty{})
	if err != nil {
		return hostsvc.HostCaps{}
	}
	var caps hostsvc.HostCaps
	_ = unmarshalJSON(resp.Payload, &caps)
	return caps
}

func (h *remoteHost) BeadsStatus(ctx context.Context, dir string) (hostsvc.BeadsStatus, error) {
	client := h.conn.Client()
	if client == nil {
		return hostsvc.BeadsStatus{}, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]string{"dir": dir})
	resp, err := client.BeadsStatus(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return hostsvc.BeadsStatus{}, err
	}
	var out hostsvc.BeadsStatus
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) GitInfo(ctx context.Context, dirs []string) (map[string]git.Info, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, err := marshalJSON(dirs)
	if err != nil {
		return nil, err
	}
	resp, err := client.GitInfo(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out map[string]git.Info
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) GitDiff(ctx context.Context, dir string, opts hostsvc.GitDiffOptions) (*git.Diff, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir, "force": opts.Force})
	resp, err := client.GitDiff(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out git.Diff
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) GitBranches(ctx context.Context, dir string) ([]string, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir})
	resp, err := client.GitBranches(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out []string
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) GitCheckout(ctx context.Context, dir, branch string) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir, "branch": branch})
	_, err := client.GitCheckout(ctx, &pb.JsonReq{Payload: b})
	return err
}

func (h *remoteHost) ListWorktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir})
	resp, err := client.ListWorktrees(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out []git.Worktree
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error) {
	client := h.conn.Client()
	if client == nil {
		return "", ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir})
	resp, err := client.WorktreeDefaultBaseRef(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return "", err
	}
	var out struct {
		BaseRef string `json:"baseRef"`
	}
	return out.BaseRef, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	resp, err := client.CreateWorktreeSession(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out hostsvc.WorktreeSessionResult
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) RemoveWorktree(ctx context.Context, req hostsvc.RemoveWorktreeRequest) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return err
	}
	_, err = client.RemoveWorktree(ctx, &pb.JsonReq{Payload: b})
	return err
}

func (h *remoteHost) LaunchTmux(ctx context.Context, req hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	resp, err := client.LaunchTmux(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out hostsvc.LaunchTmuxResult
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	resp, err := client.EnsureProjectOpencode(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out hostsvc.EnsureProjectOpencodeResult
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) StopProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return err
	}
	_, err = client.StopProjectOpencode(ctx, &pb.JsonReq{Payload: b})
	return err
}

func (h *remoteHost) RestartProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	resp, err := client.RestartProjectOpencode(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out hostsvc.EnsureProjectOpencodeResult
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) ManagedOpencodes(ctx context.Context) ([]hostsvc.ManagedOpencode, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.ManagedOpencodes(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	var out []hostsvc.ManagedOpencode
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) TmuxSessions(ctx context.Context) ([]hostsvc.TmuxSession, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.TmuxSessions(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	var out []hostsvc.TmuxSession
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) DaguStatus(ctx context.Context) dagu.Result {
	client := h.conn.Client()
	if client == nil {
		return dagu.Result{Status: dagu.Unavailable}
	}
	resp, err := client.DaguStatus(ctx, &pb.Empty{})
	if err != nil {
		return dagu.Result{Status: dagu.Unavailable}
	}
	var out dagu.Result
	if unmarshalJSON(resp.Payload, &out) != nil {
		return dagu.Result{Status: dagu.Unsupported}
	}
	return out
}

func (h *remoteHost) Projects(ctx context.Context) ([]db.ProjectStats, error) {
	// The remote returns ProjectIdentity records (identity + aggregate
	// stats); the Host interface expects ProjectStats. Map both across.
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.Projects(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	var idents []ProjectIdentity
	if err := unmarshalJSON(resp.Payload, &idents); err != nil {
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
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir})
	resp, err := client.TermWindows(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out []hostsvc.TermWindow
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) TermCreateWindow(ctx context.Context, dir string) (string, error) {
	client := h.conn.Client()
	if client == nil {
		return "", ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir})
	resp, err := client.TermCreateWindow(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return "", err
	}
	var out struct {
		Window string `json:"window"`
	}
	return out.Window, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) TermKillWindow(ctx context.Context, dir, window string) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir, "window": window})
	_, err := client.TermKillWindow(ctx, &pb.JsonReq{Payload: b})
	return err
}

// TermAttach tunnels a browser terminal to the remote's PTY over the
// bidi TerminalStream RPC: the first frame carries the window selection
// (open), then viewer keystrokes/resizes are forwarded and PTY output is
// written back to conn. Runs the shell on the remote machine (R-C).
func (h *remoteHost) TermAttach(ctx context.Context, req hostsvc.TermAttachRequest, conn hostsvc.TermConn) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
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
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.Projects(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	var idents []ProjectIdentity
	return idents, unmarshalJSON(resp.Payload, &idents)
}
