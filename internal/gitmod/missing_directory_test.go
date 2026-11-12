package gitmod

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
)

type missingDirectoryHost struct{ hostsvc.Host }

func (missingDirectoryHost) GitDiff(ctx context.Context, dir string, _ hostsvc.GitDiffOptions) (*git.Diff, error) {
	return git.GetDiff(ctx, dir, git.DiffOptions{Force: true})
}

func (missingDirectoryHost) ListWorktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	root, err := git.ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	return git.ListWorktrees(ctx, root)
}

func TestMissingDirectoryReadsReturn404OnExplicitOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deleted")
	for _, path := range []string{"/api/git/diff", "/api/worktree/list"} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(module.Guards{Auth: pass, Localhost: pass})
			calls := 0
			h.m.Owner = func(_ http.ResponseWriter, gotDir, owner string) (hostsvc.Host, bool) {
				calls++
				if gotDir != dir || owner != "rem1" {
					t.Fatalf("owner scope changed: %s %s", gotDir, owner)
				}
				return missingDirectoryHost{}, true
			}
			res := h.serve(http.MethodGet, path+"?dir="+url.QueryEscape(dir)+"&remoteId=rem1", "")
			if res.Code != http.StatusNotFound || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", res.Code, calls, res.Body)
			}
		})
	}
}
