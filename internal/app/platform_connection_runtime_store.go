package app

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/state"
)

type connectionManagedStore struct {
	store *platformconnections.RuntimeStore
}

func (s connectionManagedStore) Upsert(ctx context.Context, root string, inst local.ManagedInstance) error {
	return s.store.UpsertManagedProvider(ctx, "opencode", root, state.ManagedInstance{Endpoint: inst.Endpoint, Kind: inst.Kind, RuntimeID: inst.RuntimeID, PID: inst.PID, LaunchedAt: inst.LaunchedAt})
}
func (s connectionManagedStore) Get(ctx context.Context, root string) (local.ManagedInstance, bool, error) {
	i, ok, err := s.store.GetManagedProvider(ctx, "opencode", root)
	return local.ManagedInstance{Endpoint: i.Endpoint, Kind: i.Kind, RuntimeID: i.RuntimeID, PID: i.PID, LaunchedAt: i.LaunchedAt}, ok, err
}
func (s connectionManagedStore) Delete(ctx context.Context, root string) error {
	return s.store.DeleteManagedProvider(ctx, "opencode", root)
}
func (s connectionManagedStore) List(ctx context.Context) (map[string]local.ManagedInstance, error) {
	roots, err := s.store.ListManagedProviders(ctx, "opencode")
	if err != nil {
		return nil, err
	}
	out := make(map[string]local.ManagedInstance, len(roots))
	for _, root := range roots {
		i, ok, err := s.Get(ctx, root)
		if err != nil {
			return nil, err
		}
		if ok {
			out[root] = i
		}
	}
	return out, nil
}
