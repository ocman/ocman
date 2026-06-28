package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitinfo"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/worktree"
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

func (h *remoteHost) GitInfo(ctx context.Context, dirs []string) (map[string]gitinfo.Info, error) {
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
	var out map[string]gitinfo.Info
	return out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) GitDiff(ctx context.Context, dir string, opts hostsvc.GitDiffOptions) (*gitinfo.Diff, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir, "force": opts.Force})
	resp, err := client.GitDiff(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out gitinfo.Diff
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (h *remoteHost) ListWorktrees(ctx context.Context, dir string) ([]worktree.Entry, error) {
	client := h.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, _ := marshalJSON(map[string]any{"dir": dir})
	resp, err := client.ListWorktrees(ctx, &pb.JsonReq{Payload: b})
	if err != nil {
		return nil, err
	}
	var out []worktree.Entry
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

func (h *remoteHost) Projects(ctx context.Context) ([]db.ProjectStats, error) {
	// The remote returns ProjectIdentity records; the Host interface
	// expects ProjectStats. Map the directory across so callers that
	// only need the directory list work. Rich stats are local-only.
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
		out = append(out, db.ProjectStats{Directory: id.Dir})
	}
	return out, nil
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
