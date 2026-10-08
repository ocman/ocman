package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type interruptionMembershipHost struct {
	hostsvc.Host
	resolve func(context.Context, string) (string, error)
}

func (h *interruptionMembershipHost) ManagedOpencodeRoot(ctx context.Context, dir string) (string, error) {
	return h.resolve(ctx, dir)
}

// Lifecycle-only fixtures declare their synthetic repository identities rather
// than relying on real paths existing or on directory-containment heuristics.
func useInterruptionMembershipFixture(srv *Server) {
	srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(_ context.Context, dir string) (string, error) {
		switch dir {
		case "/repo":
			return "/repo", nil
		case "/projects/repo", "/projects/.worktrees/repo/a":
			return "/projects/repo", nil
		case "/projects/other":
			return "/projects/other", nil
		default:
			return "", git.ErrNotARepo
		}
	}})
}

func newInterruptionTestServer(t *testing.T) (*Server, *platforms.Registry) {
	srv, reg := newSessionsTestServer(t)
	useInterruptionMembershipFixture(srv)
	return srv, reg
}

func newInterruptionRawServer(t *testing.T) (*Server, *sql.DB) {
	srv, raw := testServerWithRawDB(t)
	useInterruptionMembershipFixture(srv)
	return srv, raw
}
