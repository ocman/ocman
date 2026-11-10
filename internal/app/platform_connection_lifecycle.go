package app

import (
	"context"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	claudecode "github.com/ocman/ocman/internal/platforms/claude/transport"
	codextransport "github.com/ocman/ocman/internal/platforms/codex/transport"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

func registerRetainedConnections(ctx context.Context, core *state.DB, history *statev2.DB, registry *platforms.Registry, router *platformconnections.Router, store *platformconnections.Store, owner string) error {
	rows, err := store.RetainedConnections(ctx)
	if err != nil {
		return err
	}
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		return err
	}
	for _, row := range rows {
		c := row.Connection
		if _, exists := registry.Get(platforms.ID(c.ID)); exists {
			continue
		}
		adapter, err := owned.NewRetained(catalogue, history, owner, platforms.HistoryIdentity{Platform: c.Kind, SourceKey: row.SourceKey, Name: c.Name})
		if err != nil {
			return err
		}
		keys, err := store.SourceKeys(ctx, c.ID)
		if err != nil {
			return err
		}
		adapter.WithReadSources(keys)
		if err := registry.RegisterConnection(platforms.ID(c.ID), adapter); err != nil {
			return err
		}
		if err := router.BindRead(ctx, c.ID, adapter); err != nil {
			return err
		}
	}
	return nil
}

func connectionLifecycle(factory connectionFactory, router *platformconnections.Router, keys []platforms.ID, native []platforms.Integration) hostsvc.ConnectionLifecycle {
	adapters := map[string]platforms.Integration{}
	for i, key := range keys {
		adapters[string(key)] = native[i]
	}
	return hostsvc.ConnectionLifecycle{
		Inspect: func(ctx context.Context, c platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
			result := hostsvc.ConnectionCheck{Status: "unchecked", Directory: c.Directory, Checks: []hostsvc.ConnectionCheckItem{}}
			result.Command = factory.loginCommand(c)
			result.Instructions = "Run this sign-in command on this connection's owning machine, then check again."
			var inspection platforms.ConnectionInspection
			var err error
			if p := adapters[c.ID]; p != nil {
				if inspector, ok := p.(platforms.ConnectionInspector); ok {
					inspection, err = inspector.InspectConnection(ctx)
				} else {
					return result, nil
				}
			} else {
				switch c.Kind {
				case "claudecode":
					inspection, err = claudecode.InspectConfiguration(ctx, c.Directory)
				case "codex":
					inspection, err = codextransport.InspectConfiguration(ctx, c.Directory)
				case "opencode":
					cfg := factory.openCodeConfig(c)
					auth, authErr := factory.connectionAuth(ctx, c)
					if authErr != nil {
						return result, authErr
					}
					runtime, runtimeErr := ocruntime.NewNativeConnectionRuntime(cfg, auth)
					if runtimeErr != nil {
						return result, runtimeErr
					}
					inspection, err = local.InspectOpenCodeConnection(ctx, cfg.RuntimeDir, runtime, func(ctx context.Context, endpoint string) (platforms.ConnectionInspection, error) {
						return opencode.InspectConfiguration(ctx, opencode.ConnectionConfig{Native: cfg, Auth: auth}, endpoint)
					})
				default:
					return result, nil
				}
			}
			if err != nil {
				result.Status = "error"
				return result, err
			}
			result.Account = inspection.AccountLabel
			if inspection.Message != "" {
				result.Instructions = inspection.Message
			}
			result.Status = "sign-in-required"
			if inspection.Ready {
				result.Status = "ready"
			}
			result.Checks = append(result.Checks, hostsvc.ConnectionCheckItem{Label: "Native account", OK: inspection.Ready, Detail: inspection.Message})
			return result, nil
		},
		Detach: func(ctx context.Context, c platformconnections.Connection, remove func() error) error {
			return router.Detach(ctx, c.ID, func() error {
				if p := adapters[c.ID]; p != nil {
					if err := waitConnectionIdle(ctx, factory.state, c.ID, p); err != nil {
						return err
					}
				}
				return remove()
			})
		},
	}
}

func waitConnectionIdle(ctx context.Context, db *state.DB, id string, p platforms.Integration) error {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		idle, err := connectionIdle(ctx, db, id, p)
		if err != nil {
			return err
		}
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func connectionIdle(ctx context.Context, db *state.DB, id string, p platforms.Integration) (bool, error) {
	queued, err := db.SessionsWithQueuedMessages(ctx)
	if err != nil {
		return false, err
	}
	for _, q := range queued {
		if q.Platform == id || q.Platform == string(p.ID()) {
			return false, fmt.Errorf("%w: connection has queued messages", platformconnections.ErrUnavailable)
		}
	}
	if reader, ok := p.(platforms.ReloadIdleReader); ok {
		return reader.ReloadIdle(ctx)
	}
	rows, err := p.Sessions(ctx, "", 0)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if platforms.ReloadSessionActive(row) {
			return false, nil
		}
	}
	return true, nil
}
