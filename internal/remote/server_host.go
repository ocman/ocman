package remote

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) GitInfo(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var dirs []string
	if err := unmarshalJSON(req.Payload, &dirs); err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if err := requireAbsoluteHostPath(dir, "git info directory"); err != nil {
			return nil, err
		}
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
func (s *Server) ListRepoFiles(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir     string `json:"dir"`
		Ignored bool   `json:"ignored"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(notFoundStatus(s.host.ListRepoFiles(ctx, args.Dir, args.Ignored)))
}
func (s *Server) ReadRepoFile(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir     string `json:"dir"`
		Path    string `json:"path"`
		Ignored bool   `json:"ignored"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(notFoundStatus(s.host.ReadRepoFile(ctx, args.Dir, args.Path, args.Ignored)))
}
func notFoundStatus[T any](v T, err error) (T, error) {
	if errors.Is(err, git.ErrNotARepo) || errors.Is(err, git.ErrFileNotFound) {
		return v, status.Error(codes.NotFound, err.Error())
	}
	return v, err
}
func (s *Server) GitCheckout(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	var args struct {
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	// ponytail: dirty-checkout errors are re-matched by the HTTP handler; add
	// gRPC status details if callers need a typed sentinel across the wire.
	return &pb.Empty{}, s.host.GitCheckout(ctx, args.Dir, args.Branch)
}
func (s *Server) ProjectUpstreams(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	if err := requireAbsoluteHostPath(args.Dir, "project directory"); err != nil {
		return nil, err
	}
	upstreams, err := s.host.ProjectUpstreams(ctx, args.Dir)
	if errors.Is(err, git.ErrNotARepo) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err == nil && upstreams != nil {
		for i := range upstreams.Remotes {
			upstreams.Remotes[i].URL = ""
		}
	}
	return jsonResp(upstreams, err)
}
func (s *Server) FetchPRHead(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args hostsvc.FetchPRHeadRequest
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	if err := requireAbsoluteHostPath(args.RepoRoot, "repository root"); err != nil {
		return nil, err
	}
	branch, err := s.host.FetchPRHead(ctx, args)
	return jsonResp(map[string]string{"branch": branch}, err)
}
func requireAbsoluteHostPath(path, label string) error {
	if !filepath.IsAbs(path) {
		return status.Errorf(codes.InvalidArgument, "%s must be absolute", label)
	}
	return nil
}
func (s *Server) ListWorktrees(ctx context.Context, req *pb.JsonReq) (*pb.JsonResp, error) {
	var args struct {
		Dir string `json:"dir"`
	}
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	trees, err := s.host.ListWorktrees(ctx, args.Dir)
	if errors.Is(err, git.ErrNotARepo) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return jsonResp(trees, err)
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
