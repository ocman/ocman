package owned

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// recordStoredCreation publishes only acknowledged creation metadata. It never
// invents a native result, removes content, or replaces an ingested snapshot.
func (a *Adapter) recordStoredCreation(ctx context.Context, row sessioncatalog.Session) error {
	instance, err := a.history.FindInstance(ctx, a.owner, a.source.Platform, a.source.SourceKey)
	if errors.Is(err, statev2.ErrNotFound) {
		instance = statev2.Instance{ID: statev2.SourceInstanceID(a.owner, a.source.Platform, a.source.SourceKey), OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey, Name: a.source.Name}
		err = a.history.RegisterInstance(ctx, instance)
	}
	if err != nil {
		return err
	}
	key := statev2.Key{InstanceID: instance.ID, SessionID: row.ID}
	existing, err := a.history.SessionState(ctx, key)
	if err != nil {
		return err
	}
	if existing.Revision > 0 {
		return nil
	}
	_, err = a.history.Commit(ctx, statev2.Batch{Session: statev2.Session{Key: key, Title: row.Title, Directory: row.Directory, ParentID: row.ParentID, CreatedAt: row.CreatedAt, UpdatedAt: row.CreatedAt, ReadFormat: statev2.ConversationReadFormat, Extensions: json.RawMessage(`{"RuntimeBootstrap":true,"SourceChild":false}`)}})
	if errors.Is(err, statev2.ErrConflict) {
		return nil
	} // ingestion won the race
	return err
}
