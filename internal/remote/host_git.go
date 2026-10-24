package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"google.golang.org/grpc"
)

func (h *remoteHost) GitInfo(ctx context.Context, dirs []string) (map[string]git.Info, error) {
	return hostCall[map[string]git.Info](ctx, h.conn, "GitInfo", dirs)
}

func (h *remoteHost) GitDiff(ctx context.Context, dir string, opts hostsvc.GitDiffOptions) (*git.Diff, error) {
	return ptr(hostCall[git.Diff](ctx, h.conn, "GitDiff", map[string]any{"dir": dir, "force": opts.Force}))
}

func (h *remoteHost) GitBranches(ctx context.Context, dir string) ([]string, error) {
	return hostCall[[]string](ctx, h.conn, "GitBranches", map[string]any{"dir": dir})
}

func (h *remoteHost) ListRepoFiles(ctx context.Context, dir string, ignored bool) (*git.FileList, error) {
	out, err := hostCall[git.FileList](ctx, h.conn, "ListRepoFiles", map[string]any{"dir": dir, "ignored": ignored}, grpc.MaxCallRecvMsgSize(maxRepoResponseBytes))
	return ptr(out, notFoundAs(err, git.ErrNotARepo))
}

func (h *remoteHost) ReadRepoFile(ctx context.Context, dir, path string, ignored bool) (*git.FileContent, error) {
	out, err := hostCall[git.FileContent](ctx, h.conn, "ReadRepoFile", map[string]any{"dir": dir, "path": path, "ignored": ignored}, grpc.MaxCallRecvMsgSize(maxRepoResponseBytes))
	return ptr(out, notFoundAs(err, git.ErrFileNotFound))
}

// maxRepoResponseBytes lifts gRPC's 4 MiB default for the file RPCs. The
// producer bounds the payload: git.MaxListedBytes (8 MiB) of paths or
// git.MaxFileBytes (1 MiB) of text or MaxImageBytes (10 MiB) of base64
// image bytes. JSON escaping grows a text byte to
// at most 6 (\u003c), plus per-entry quotes and commas.
const maxRepoResponseBytes = 6*8<<20 + 16<<20

func (h *remoteHost) GitCheckout(ctx context.Context, dir, branch string) error {
	return hostExec(ctx, h.conn, "GitCheckout", map[string]any{"dir": dir, "branch": branch})
}
