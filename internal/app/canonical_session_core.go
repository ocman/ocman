package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/platforms/ownedjudge"
	"github.com/ocman/ocman/internal/pluginsmod"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/startup"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

func canonicalStartupError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("preparing canonical session core: %w", err)
}

// composeCanonicalSessionCore does bounded setup only. Registered adapters must
// not have consumers until work completes. A completed cutover needs indexed
// receipts and pending-proof checks, never full inventories or parent walks.
func composeCanonicalSessionCore(ctx context.Context, core *state.DB, history *statev2.DB, registry *platforms.Registry, native []platforms.Integration, owner string) (func(context.Context) error, error) {
	if registry == nil {
		return nil, sessioncatalog.ErrInvalid
	}
	startup.Report(ctx, "Preparing canonical session core", 0, 0)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		return nil, err
	}
	prepared, err := owned.BuildCore(ctx, core, history, catalogue, owner, native)
	if err != nil {
		return nil, err
	}
	references, err := owned.NewReferences(core)
	if err != nil {
		return nil, err
	}
	var sources, scopes []string
	for i, p := range prepared {
		scopes = append(scopes, string(p.ID()))
		source, err := p.(platforms.HistoryReader).HistoryIdentity(ctx)
		if err != nil {
			sources = append(sources, string(p.ID())+"\x00unavailable")
		} else {
			sources = append(sources, source.Platform+"\x00"+source.SourceKey)
		}
		registry.Register(ownedjudge.Wrap(p.(*owned.Adapter), native[i]))
	}
	if err := registry.EnableCanonicalSessions(); err != nil {
		return nil, err
	}
	sort.Strings(sources)
	raw, _ := json.Marshal(sources)
	signature := fmt.Sprintf("v1:%x:", sha256.Sum256(raw))
	receipt, err := history.PreparationReceipt(ctx, owner)
	if err != nil {
		return nil, err
	}
	complete, err := references.PreparationComplete(ctx, owner, history.FilePath(), signature+receipt)
	if err != nil {
		return nil, err
	}
	if complete && receipt != "" {
		pending, err := references.HasResolvableLocalReferences(ctx, owner, scopes)
		if err != nil || !pending {
			return nil, err
		}
	}
	return func(ctx context.Context) error {
		ctx = state.WithSessionMigrationBackup(ctx)
		if err := owned.PrepareBuiltCore(ctx, core, history, catalogue, owner, prepared); err != nil {
			return err
		}
		resolve := func(ctx context.Context, scope, reference string) (string, error) {
			return references.PrepareReference(ctx, catalogue, owner, scope, reference)
		}
		startup.Report(ctx, "Migrating plugin session references", 0, 0)
		if err := pluginsmod.NewStore(core).MigrateSessionReferences(ctx, core, resolve); err != nil {
			return err
		}
		receipt := uuid.NewString()
		if err := history.CompletePreparation(ctx, owner, receipt); err != nil {
			return err
		}
		return references.CompletePreparation(ctx, owner, history.FilePath(), signature+receipt)
	}, nil
}
