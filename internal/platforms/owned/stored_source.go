package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
)

// BindStoredHistory routes invalidation and bounded read-your-writes waits to
// ingestion. Installed before serving.
func (a *Adapter) BindStoredHistory(hooks platforms.StoredHistoryHooks) {
	a.historyWait, a.historyNotify, a.historyRefresh = hooks.Await, hooks.Notify, hooks.Refresh
}

// storedIdentity validates historical scope without treating source availability
// or a native binding as authority to execute. Mutations still use nativeID.
func (a *Adapter) storedIdentity(ctx context.Context, id string) (sessioncatalog.Session, error) {
	if !sessioncatalog.IsID(id) {
		return sessioncatalog.Session{}, sessioncatalog.ErrInvalid
	}
	row, err := a.store.Get(ctx, id)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		return row, platforms.ErrNotFound
	}
	if err != nil {
		return row, err
	}
	return a.validateStoredIdentity(id, row)
}

func (a *Adapter) validateStoredIdentity(id string, row sessioncatalog.Session) (sessioncatalog.Session, error) {
	if row.OwnerID != a.owner || row.Purged {
		return row, platforms.ErrNotFound
	}
	if row.Platform != a.source.Platform {
		return row, platforms.ErrHistoryUnavailable
	}
	if row.CreationState != sessioncatalog.Ready {
		return row, sessioncatalog.ErrCreating
	}
	if row.SourceKey == a.source.SourceKey && a.unavailable == nil {
		a.byID.Store(id, row.NativeID)
	} else {
		a.byID.Delete(id)
	}
	return row, nil
}

func (a *Adapter) storedKey(ctx context.Context, id string) (statev2.Key, error) {
	row, err := a.storedIdentity(ctx, id)
	if err != nil {
		return statev2.Key{}, err
	}
	instance, err := a.history.FindInstance(ctx, a.owner, a.source.Platform, row.SourceKey)
	if errors.Is(err, statev2.ErrNotFound) {
		err = platforms.ErrHistoryUnavailable
	}
	return statev2.Key{InstanceID: instance.ID, SessionID: id}, err
}

// storedReadInstances selects the retained sources a read covers. Aggregate
// reads of a platform with nothing ingested yet are empty, never a native
// fallback; a specific session resolves to its own physical source.
func (a *Adapter) storedReadInstances(ctx context.Context, id string) ([]statev2.Instance, error) {
	if id == "" {
		return a.history.Instances(ctx, a.owner, a.source.Platform)
	}
	key, err := a.storedKey(ctx, id)
	if err != nil {
		return nil, err
	}
	instance, err := a.history.Instance(ctx, key.InstanceID)
	return []statev2.Instance{instance}, err
}

func (a *Adapter) storedConfig(directory string) *platforms.ConversationHead {
	if config, ok := a.native.(platforms.StoredReadConfigurer); ok {
		if head := config.StoredReadConfig(directory); head != nil {
			return head
		}
	}
	return &platforms.ConversationHead{DirectoryDefaults: true}
}
