package server

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
)

type runtimeLifecyclePlatform struct {
	*interruptionLifecyclePlatform
	platforms.RuntimeSessionReader
}

// The raw database is the persisted source; it wins over the fixture's
// lifecycle-derived rows.
func (p *runtimeLifecyclePlatform) GetSessionIdentities(ctx context.Context) ([]db.Session, error) {
	return p.RuntimeSessionReader.GetSessionIdentities(ctx)
}

func (p *runtimeLifecyclePlatform) GetSessionLifecycle(ctx context.Context, id string) (db.SessionLifecycle, error) {
	return p.RuntimeSessionReader.GetSessionLifecycle(ctx, id)
}

func registerRuntimeLifecycle(srv *Server, p *interruptionLifecyclePlatform) {
	srv.registry.Register(&runtimeLifecyclePlatform{p, srv.db})
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
