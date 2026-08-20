package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// #532: every ensure path must fold a managed worktree directory back
// to the project root before EnsureProjectOpencode, or a worktree
// session launches a second opencode instance per worktree instead of
// reusing the project's single instance. The queue path already folds
// (see queue_test.go); these cover the MCP and scheduled-prompt paths.

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

func TestManagedPromptSessionsFoldsWorktreeDir(t *testing.T) {
	srv := testServer(t)
	var ensured string
	var created platforms.CreateSessionRequest
	srv.hostRouter = hostsvc.NewRouter(&promptEnsureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir}, nil
	}})
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		created = req
		return &platforms.CreateSessionResponse{ID: "scheduled-session"}, nil
	}})
	srv.registry = reg

	_, _, err := (managedPromptSessions{srv}).CreateScheduledSession(t.Context(), "local", "/home/u/.worktrees/proj/feat")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if ensured != "/home/u/proj" {
		t.Fatalf("ensured dir = %q, want the folded project root /home/u/proj", ensured)
	}
	// The session itself still works in the worktree directory.
	if created.Directory != "/home/u/.worktrees/proj/feat" {
		t.Fatalf("created.Directory = %q, want the raw worktree dir", created.Directory)
	}
}
