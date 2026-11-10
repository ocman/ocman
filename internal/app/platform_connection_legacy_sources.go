package app

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/statev2"
)

func bindMigratedHistorySources(ctx context.Context, store *platformconnections.Store, history *statev2.DB, owner string) error {
	collection, err := store.Read(ctx)
	if err != nil {
		return err
	}
	for _, c := range collection.Connections {
		if c.LegacyKind == "" {
			continue
		}
		allowed, err := store.LegacySourceImportAllowed(ctx, c.ID)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		instances, err := history.Instances(ctx, owner, c.Kind)
		if err != nil {
			return err
		}
		for _, instance := range instances {
			if existing, err := store.SourceConnection(ctx, c.Kind, instance.SourceKey); err == nil {
				if existing != c.ID {
					return platformconnections.ErrAlreadyBound
				}
				continue
			} else if !errors.Is(err, platformconnections.ErrNotFound) {
				return err
			}
			if err := store.BindSource(ctx, c.ID, c.Kind, instance.SourceKey); err != nil {
				return err
			}
		}
	}
	return nil
}
