package remote

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

func (h *remoteHost) ListRepoFiles(ctx context.Context, dir string, ignored bool) (*git.FileList, error) {
	var out git.FileList
	return &out, h.callFiles(ctx, git.ErrNotARepo, &out, func(c pb.OcmanClient, req *pb.JsonReq) (*pb.JsonResp, error) {
		return c.ListRepoFiles(ctx, req, grpc.MaxCallRecvMsgSize(maxRepoResponseBytes))
	}, map[string]any{"dir": dir, "ignored": ignored})
}

func (h *remoteHost) ReadRepoFile(ctx context.Context, dir, path string, ignored bool) (*git.FileContent, error) {
	var out git.FileContent
	return &out, h.callFiles(ctx, git.ErrFileNotFound, &out, func(c pb.OcmanClient, req *pb.JsonReq) (*pb.JsonResp, error) {
		return c.ReadRepoFile(ctx, req, grpc.MaxCallRecvMsgSize(maxRepoResponseBytes))
	}, map[string]any{"dir": dir, "path": path, "ignored": ignored})
}

// maxRepoResponseBytes lifts gRPC's 4 MiB default for the file RPCs. The
// producer bounds the payload: git.MaxListedBytes (8 MiB) of paths or
// git.MaxFileBytes (1 MiB) of text or MaxImageBytes (10 MiB) of base64
// image bytes. JSON escaping grows a text byte to
// at most 6 (\u003c), plus per-entry quotes and commas.
const maxRepoResponseBytes = 6*8<<20 + 16<<20

// callFiles runs a file RPC, restoring notFound from codes.NotFound.
func (h *remoteHost) callFiles(ctx context.Context, notFound error, out any, call func(pb.OcmanClient, *pb.JsonReq) (*pb.JsonResp, error), args map[string]any) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	b, _ := marshalJSON(args)
	resp, err := call(client, &pb.JsonReq{Payload: b})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return errors.Join(notFound, err)
		}
		return err
	}
	return unmarshalJSON(resp.Payload, out)
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
