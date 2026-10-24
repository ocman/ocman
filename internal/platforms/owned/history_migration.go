package owned

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/startup"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// MigrateHistory adopts retained history from every physical source owned by
// this machine, including inactive installations. It never contacts a platform
// or grants execution authority to an inactive binding. Call before starting
// catalogue indexing, ingestion or request handling. Each database operation is
// durable and replayable, so a crash between catalogue adoption and history
// rekeying retains the same IDs on the next startup.
func MigrateHistory(ctx context.Context, catalogue *sessioncatalog.Store, history *statev2.DB, owner string) error {
	if catalogue == nil || history == nil || owner == "" {
		return sessioncatalog.ErrInvalid
	}
	ready, err := history.SessionRekeyInventoryReady(ctx, owner)
	if err != nil {
		return err
	}
	if ready {
		return applyHistoryPlan(ctx, history, owner)
	}
	processed := 0
	var cursor statev2.Key
	for {
		page, err := history.MetadataPage(ctx, owner, cursor, 500)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		plans := map[string]map[string]string{}
		for _, metadata := range page {
			mapping := plans[metadata.Instance.ID]
			if mapping == nil {
				mapping = map[string]string{}
				plans[metadata.Instance.ID] = mapping
			}
			if err := migrateMetadata(ctx, catalogue, metadata, mapping); err != nil {
				return fmt.Errorf("adopting retained session identity: %w", err)
			}
		}
		for instance, mapping := range plans {
			if err := history.PlanSessionRekeys(ctx, instance, mapping); err != nil {
				return err
			}
		}
		cursor = page[len(page)-1].Session.Key
		processed += len(page)
		startup.Report(ctx, "Inventorying retained conversations", processed, 0)
	}
	// No history keys change during the inventory above. Rekeying mid-page
	// would invalidate its lexical cursor and could skip retained sessions.
	if err := history.SetSessionRekeyInventoryReady(ctx, owner, true); err != nil {
		return err
	}
	return applyHistoryPlan(ctx, history, owner)
}

func applyHistoryPlan(ctx context.Context, history *statev2.DB, owner string) error {
	if err := history.ApplyPlannedSessionRekeys(ctx, owner, func(completed int) {
		startup.Report(ctx, "Rekeying conversation sources", completed, 0)
	}); err != nil {
		return err
	}
	return history.SetSessionRekeyInventoryReady(ctx, owner, false)
}

func migrateMetadata(ctx context.Context, catalogue *sessioncatalog.Store, metadata statev2.SessionMetadata, mapping map[string]string) error {
	source := sessioncatalog.Source{OwnerID: metadata.Instance.OwnerID, Platform: metadata.Instance.Platform, SourceKey: metadata.Instance.SourceKey}
	id := metadata.Session.SessionID
	if sessioncatalog.IsID(id) {
		row, err := catalogue.Get(ctx, id)
		if err != nil {
			return err
		}
		if row.OwnerID != source.OwnerID || row.Platform != source.Platform || row.SourceKey != source.SourceKey {
			return sessioncatalog.ErrBindingConflict
		}
		return nil
	}
	_, err := catalogue.Reconcile(ctx, source, []sessioncatalog.NativeSession{{ID: id, ParentID: metadata.Session.ParentID, Directory: metadata.Session.Directory, Title: metadata.Session.Title, CreatedAt: metadata.Session.CreatedAt}})
	if err != nil {
		return err
	}
	row, err := catalogue.FindBinding(ctx, sessioncatalog.Binding{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: id})
	if err != nil {
		return err
	}
	if row.CreationState != sessioncatalog.Ready {
		return sessioncatalog.ErrCreating
	}
	mapping[id] = row.ID
	if err := catalogue.RetainLegacyKeys(ctx, source.OwnerID, row.ID); err != nil {
		return err
	}
	if parent := metadata.Session.ParentID; parent != "" {
		if !sessioncatalog.IsID(row.ParentID) {
			return sessioncatalog.ErrCreating
		}
		mapping[parent] = row.ParentID
	}
	return nil
}
