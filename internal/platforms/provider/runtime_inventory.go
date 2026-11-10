package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/state"
)

// PersistedRuntimeSessions reads every recorded supervisor without attaching,
// launching, draining or adopting it. An unreadable endpoint is absent only
// when the owner confirms its tracked process has stopped.
func PersistedRuntimeSessions(ctx context.Context, platform string, store interface {
	ListManagedProviders(context.Context, string) ([]string, error)
	GetManagedProvider(context.Context, string, string) (state.ManagedInstance, bool, error)
}, runtime interface {
	Alive(context.Context, *ocruntime.Instance) (bool, error)
}) ([]db.Session, error) {
	if store == nil {
		return nil, nil
	}
	if runtime == nil {
		return nil, fmt.Errorf("supervisor liveness probe unavailable")
	}
	roots, err := store.ListManagedProviders(ctx, platform)
	if err != nil {
		return nil, err
	}
	var rows []db.Session
	for _, root := range roots {
		row, exists, err := store.GetManagedProvider(ctx, platform, root)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		adapter, probeErr := New(ctx, strings.TrimPrefix(row.Endpoint, "unix://"))
		var sessions []db.Session
		if probeErr == nil {
			if string(adapter.ID()) != platform {
				adapter.Close()
				return nil, fmt.Errorf("persisted supervisor platform changed")
			}
			sessions, probeErr = adapter.RuntimeSessions(ctx)
			adapter.Close()
		}
		if probeErr != nil {
			inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root}
			alive, err := runtime.Alive(ctx, inst)
			if err != nil || alive {
				return nil, fmt.Errorf("cannot verify persisted %s supervisor for %s: %w", platform, root, probeErr)
			}
			continue
		}
		rows = append(rows, sessions...)
	}
	return rows, ctx.Err()
}
