package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/platforms/ownedjudge"
	"github.com/NoUseFreak/ocman/internal/pluginsmod"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/startup"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func canonicalStartupError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("preparing canonical session core: %w", err)
}

// prepareCanonicalSessionCore composes all pre-serving identity migrations.
// Startup must not serve or run workers if any step fails. The caller invokes
// this before server construction, source ingestion and remote registration.
func prepareCanonicalSessionCore(ctx context.Context, core *state.DB, history *statev2.DB, registry *platforms.Registry, native []platforms.Integration, owner string) error {
	if registry == nil {
		return sessioncatalog.ErrInvalid
	}
	startup.Report(ctx, "Preparing canonical session core", 0, 0)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		return err
	}
	prepared, err := owned.PrepareCore(ctx, core, history, catalogue, owner, native)
	if err != nil {
		return err
	}
	references, err := owned.NewReferences(core)
	if err != nil {
		return err
	}
	resolve := func(ctx context.Context, scope, reference string) (string, error) {
		return references.PrepareReference(ctx, catalogue, owner, scope, reference)
	}
	startup.Report(ctx, "Migrating plugin session references", 0, 0)
	if err := pluginsmod.NewStore(core).MigrateSessionReferences(ctx, core, resolve); err != nil {
		return err
	}
	for i, p := range prepared {
		registry.Register(ownedjudge.Wrap(p.(*owned.Adapter), native[i]))
	}
	startup.Report(ctx, "Starting application services", 0, 0)
	return registry.EnableCanonicalSessions()
}
