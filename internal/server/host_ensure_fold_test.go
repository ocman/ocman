package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

// #532: every ensure path must fold a managed worktree directory back
// to the project root before EnsureProjectOpencode, or a worktree
// session launches a second opencode instance per worktree instead of
// reusing the project's single instance. The queue path already folds
// (see queue_test.go); this covers the MCP path.

func TestEnsureProjectOpencodePortFoldsWorktreeDir(t *testing.T) {
	srv := testServer(t)
	var ensured string
	srv.hostRouter = hostsvc.NewRouter(&ensureStubHost{
		ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
			ensured = req.ProjectDir
			return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir}, nil
		},
	})

	port, err := srv.ensureProjectOpencodePort(t.Context(), "/home/u/.worktrees/proj/feat")
	if err != nil || port != "5599" {
		t.Fatalf("port=%q err=%v", port, err)
	}
	if ensured != "/home/u/proj" {
		t.Fatalf("ensured dir = %q, want the folded project root /home/u/proj", ensured)
	}
}
