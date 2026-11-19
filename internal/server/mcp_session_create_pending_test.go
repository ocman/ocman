package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

// An allocation whose outcome is uncertain must reach the tool with its
// recovery identity so the agent is told not to create a duplicate.
func TestMCPCreateSessionKeepsPendingRecoveryIdentity(t *testing.T) {
	srv := testServer(t)
	host := &mcpCreateHost{ensureHost: &ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
		return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:6620", RepoRoot: req.ProjectDir}, nil
	}}}
	srv.hostRouter = hostsvc.NewRouter(host)
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{PlatformID: "opencode", DirectoryCatalogFn: mcpTestCatalog("p/m"),
		CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return nil, &platforms.CreationPendingError{SessionID: "ocm_pending", Err: context.Canceled}
		}})
	srv.registry = reg
	worktree := false
	_, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/src/ocman", Platform: "opencode", Model: "p/m", Worktree: &worktree})
	var pending *platforms.CreationPendingError
	var staged *internalmcp.SessionCreateStageError
	if !errors.As(err, &pending) || pending.SessionID != "ocm_pending" || !errors.As(err, &staged) || staged.Stage != "session allocation" {
		t.Fatalf("pending creation error = %v", err)
	}
	if strings.Contains(err.Error(), "invalid session request") {
		t.Fatalf("pending creation reported as invalid input: %v", err)
	}
}
