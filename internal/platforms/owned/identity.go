// Package owned keeps native session identifiers behind the platform interface.
// Callers use only owner-allocated Ocman identities; source payloads and native
// message/part identifiers retain their original representation.
package owned

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/sidebar"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type sourceReader interface {
	HistoryIdentity(context.Context) (platforms.HistoryIdentity, error)
}

// Adapter is the canonical interface around one physical owner-local platform.
// Remote adapters already receive canonical IDs from their authenticated owner
// and must never allocate a second identity on a hub.
type Adapter struct {
	native            platforms.Integration
	store             *sessioncatalog.Store
	owner             string
	source            platforms.HistoryIdentity
	reader            sourceReader
	unavailable       error
	byID              sync.Map
	lifecycle         lifecycleEvents
	history           *statev2.DB
	historyWait       func(context.Context, string) bool
	historyNotify     func(string)
	historyRefresh    func(context.Context, string) error
	sidebarMu         sync.Mutex
	sidebarReader     *sidebar.Reader
	sidebarGeneration string
	background        backgroundState
}

// New binds one owner-local integration to its catalogue and persisted read
// model. Persisted reads always come from history; there is no native fallback.
func New(ctx context.Context, native platforms.Integration, store *sessioncatalog.Store, history *statev2.DB, owner string) (*Adapter, error) {
	if native == nil || store == nil || history == nil || owner == "" {
		return nil, sessioncatalog.ErrInvalid
	}
	if remote, _ := platforms.SplitPlatformID(string(native.ID())); remote != "" {
		return nil, sessioncatalog.ErrInvalid
	}
	reader, ok := native.(sourceReader)
	if !ok {
		return nil, fmt.Errorf("%w: platform has no physical source", platforms.ErrHistoryUnavailable)
	}
	source, err := reader.HistoryIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if source.Platform != string(native.ID()) || source.SourceKey == "" {
		return nil, sessioncatalog.ErrInvalid
	}
	return newAdapter(native, store, history, owner, source, reader, nil), nil
}

func newAdapter(native platforms.Integration, store *sessioncatalog.Store, history *statev2.DB, owner string, source platforms.HistoryIdentity, reader sourceReader, unavailable error) *Adapter {
	a := &Adapter{native: native, store: store, history: history, owner: owner, source: source, reader: reader, unavailable: unavailable}
	a.bindStoredHaltIdentity()
	return a
}

func (a *Adapter) checkSource(ctx context.Context) error {
	if a.unavailable != nil {
		return fmt.Errorf("%w: source unavailable: %w", platforms.ErrHistoryUnavailable, a.unavailable)
	}
	source, err := a.reader.HistoryIdentity(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", platforms.ErrHistoryUnavailable, err)
	}
	if source.Platform != a.source.Platform || source.SourceKey != a.source.SourceKey {
		return fmt.Errorf("%w: platform source changed", platforms.ErrHistoryUnavailable)
	}
	return nil
}

func (a *Adapter) nativeID(ctx context.Context, id string) (string, error) {
	if identity.IsPendingReference(id) {
		return "", platforms.ErrPlatformUnreachable
	}
	if !sessioncatalog.IsID(id) {
		return "", fmt.Errorf("%w: canonical session ID required", sessioncatalog.ErrInvalid)
	}
	if err := a.checkSource(ctx); err != nil {
		return "", err
	}
	row, err := a.store.Get(ctx, id)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		return "", platforms.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if row.OwnerID != a.owner || row.Purged {
		return "", platforms.ErrNotFound
	}
	if row.Platform != string(a.ID()) || row.SourceKey != a.source.SourceKey {
		return "", platforms.ErrHistoryUnavailable
	}
	switch row.CreationState {
	case sessioncatalog.Creating, sessioncatalog.Configuring:
		return "", sessioncatalog.ErrCreating
	case sessioncatalog.CreationFailed:
		return "", sessioncatalog.ErrCreationFailed
	case sessioncatalog.Ready:
	default:
		return "", sessioncatalog.ErrTransition
	}
	a.byID.Store(id, row.NativeID)
	return row.NativeID, nil
}

func (a *Adapter) binding(nativeID string) sessioncatalog.Binding {
	return sessioncatalog.Binding{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey, NativeID: nativeID}
}

// canonical projects a native ID seen in an event, prompt or journal. An
// unknown ID is bound only after the integration proves its source has it, so
// a machine-wide journal or a foreign reference never acquires an identity.
func (a *Adapter) canonical(ctx context.Context, nativeID string) (sessioncatalog.Session, error) {
	if nativeID == "" {
		return sessioncatalog.Session{}, platforms.ErrNotFound
	}
	if err := a.checkSource(ctx); err != nil {
		return sessioncatalog.Session{}, err
	}
	row, err := a.store.FindBinding(ctx, a.binding(nativeID))
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		probe, ok := a.native.(platforms.SessionIdentityProbe)
		if !ok {
			return row, platforms.ErrHistoryUnavailable
		}
		metadata, probeErr := probe.SessionIdentityMetadata(ctx, nativeID)
		if probeErr != nil {
			return row, probeErr
		}
		if metadata == nil || metadata.ID != nativeID {
			return row, platforms.ErrNotFound
		}
		if importErr := a.importRows(ctx, []db.Session{*metadata}); importErr != nil {
			return row, importErr
		}
		row, err = a.store.FindBinding(ctx, a.binding(nativeID))
		if errors.Is(err, sessioncatalog.ErrNotFound) {
			return row, sessioncatalog.ErrCreating
		}
	}
	if err != nil {
		return row, err
	}
	if row.Purged {
		return row, platforms.ErrNotFound
	}
	if row.CreationState != sessioncatalog.Ready {
		return row, sessioncatalog.ErrCreating
	}
	a.byID.Store(row.ID, row.NativeID)
	return row, nil
}

func (a *Adapter) importRows(ctx context.Context, rows []db.Session) error {
	if err := a.checkSource(ctx); err != nil {
		return err
	}
	metadata := make([]sessioncatalog.NativeSession, len(rows))
	for i, row := range rows {
		metadata[i] = sessioncatalog.NativeSession{ID: row.ID, ParentID: row.ParentID, Directory: row.Directory, Title: row.Title, CreatedAt: row.TimeCreated}
	}
	_, err := a.store.Reconcile(ctx, sessioncatalog.Source{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}, metadata)
	if err == nil {
		a.flushLifecycle(ctx)
	}
	return err
}

func (a *Adapter) session(ctx context.Context, row db.Session) (db.Session, error) {
	identity, err := a.canonical(ctx, row.ID)
	if err != nil {
		return db.Session{}, err
	}
	row.ID, row.ParentID, row.Platform = identity.ID, identity.ParentID, string(a.ID())
	if identity.Origin == sessioncatalog.Routine {
		row.RoutineID = identity.OriginID
	}
	return row, nil
}
