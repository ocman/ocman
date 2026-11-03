package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
)

type runtimeLifecyclePlatform struct {
	*interruptionLifecyclePlatform
	platforms.RuntimeSessionReader
}

func registerRuntimeLifecycle(srv *Server, p *interruptionLifecyclePlatform) {
	srv.registry.Register(&runtimeLifecyclePlatform{p, srv.db})
}

func newInterruptionTestServer(t *testing.T) (*Server, *platforms.Registry) {
	return newSessionsTestServer(t)
}

func newInterruptionRawServer(t *testing.T) (*Server, *sql.DB) {
	return testServerWithRawDB(t)
}

// beginStoredReplacementStop runs the BeforeStop hook with the cleanup handle
// recorded for root in managed_opencode, or none when no row exists.
func beginStoredReplacementStop(ctx context.Context, srv *Server, root string) error {
	var inst *ocruntime.Instance
	if srv.stateDB != nil {
		stored, found, err := srv.stateDB.GetManagedOpencode(ctx, root)
		if err != nil {
			return err
		}
		if found {
			inst = &ocruntime.Instance{Endpoint: stored.Endpoint, Kind: stored.Kind, ID: stored.RuntimeID, PID: stored.PID, RepoRoot: root}
		}
	}
	return srv.beginOpencodeReplacementStopWithInstance(ctx, root, inst)
}
