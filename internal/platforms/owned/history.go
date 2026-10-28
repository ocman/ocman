package owned

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

var _ platforms.HistoryReader = (*Adapter)(nil)

func (a *Adapter) HistoryIdentity(ctx context.Context) (platforms.HistoryIdentity, error) {
	if err := a.checkSource(ctx); err != nil {
		return platforms.HistoryIdentity{}, err
	}
	return a.source, nil
}

// historyIdentity can allocate a metadata-only identity for retained or deleted
// source records without requiring the session to appear in a browsing list.
// Reservations still fence discovery until their native result is known.
func (a *Adapter) historyIdentity(ctx context.Context, nativeID string) (sessioncatalog.Session, error) {
	if nativeID == "" {
		return sessioncatalog.Session{}, sessioncatalog.ErrInvalid
	}
	if err := a.checkSource(ctx); err != nil {
		return sessioncatalog.Session{}, err
	}
	row, err := a.store.FindBinding(ctx, a.binding(nativeID))
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		if _, err := a.store.Reconcile(ctx, sessioncatalog.Source{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}, []sessioncatalog.NativeSession{{ID: nativeID}}); err != nil {
			return row, err
		}
		row, err = a.store.FindBinding(ctx, a.binding(nativeID))
		if errors.Is(err, sessioncatalog.ErrNotFound) {
			return a.resolveDeferredHistoryIdentity(ctx, nativeID)
		}
	}
	if err != nil {
		return row, err
	}
	if row.CreationState != sessioncatalog.Ready {
		return row, sessioncatalog.ErrCreating
	}
	return row, nil
}

// A metadata-only discovery cannot distinguish an old external session from
// an in-flight create. Probe scalar metadata before deferring it so unrelated
// reservations do not indefinitely block complete-history analytics.
func (a *Adapter) resolveDeferredHistoryIdentity(ctx context.Context, nativeID string) (sessioncatalog.Session, error) {
	probe, ok := a.native.(platforms.SessionIdentityProbe)
	if !ok {
		return sessioncatalog.Session{}, sessioncatalog.ErrCreating
	}
	metadata, err := probe.SessionIdentityMetadata(ctx, nativeID)
	if errors.Is(err, platforms.ErrNotFound) {
		return sessioncatalog.Session{}, sessioncatalog.ErrCreating
	}
	if err != nil {
		return sessioncatalog.Session{}, err
	}
	if metadata == nil || metadata.ID != nativeID {
		return sessioncatalog.Session{}, sessioncatalog.ErrInvalid
	}
	if err := a.importRows(ctx, []db.Session{*metadata}); err != nil {
		return sessioncatalog.Session{}, err
	}
	row, err := a.store.FindBinding(ctx, a.binding(nativeID))
	if errors.Is(err, sessioncatalog.ErrNotFound) || err == nil && row.CreationState != sessioncatalog.Ready {
		return row, sessioncatalog.ErrCreating
	}
	return row, err
}

func (a *Adapter) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	reader, ok := a.native.(platforms.HistoryReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	rows, err := reader.HistoryInventory(ctx)
	var incomplete *platforms.HistoryDeferred
	if err != nil && !errors.As(err, &incomplete) {
		return nil, err
	}
	out := make([]db.HistoryEntry, 0, len(rows))
	deferred := 0
	if incomplete != nil {
		deferred = incomplete.Count
	}
	seen := make(map[string]bool, len(rows))
	for _, entry := range rows {
		if seen[entry.ID] {
			return nil, fmt.Errorf("duplicate native history identity: %w", sessioncatalog.ErrInvalid)
		}
		seen[entry.ID] = true
		row, err := a.historyIdentity(ctx, entry.ID)
		if errors.Is(err, sessioncatalog.ErrCreating) {
			deferred++
			continue
		}
		if err != nil {
			return nil, err
		}
		if row.Purged {
			continue
		}
		entry.ID = row.ID
		out = append(out, entry)
	}
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	a.flushLifecycle(ctx)
	if deferred > 0 {
		return out, &platforms.HistoryDeferred{Count: deferred, Err: err}
	}
	return out, nil
}

func (a *Adapter) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	reader, ok := a.native.(platforms.HistoryReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	snapshot, err := reader.HistorySnapshot(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	return a.historySnapshot(ctx, nativeID, snapshot)
}

func (a *Adapter) historySnapshot(ctx context.Context, nativeID string, snapshot *db.HistorySnapshot) (*db.HistorySnapshot, error) {
	if snapshot == nil || snapshot.Session.ID != nativeID {
		return nil, fmt.Errorf("native history snapshot identity mismatch: %w", sessioncatalog.ErrInvalid)
	}
	if err := a.importRows(ctx, []db.Session{snapshot.Session}); err != nil {
		return nil, err
	}
	row, err := a.session(ctx, snapshot.Session)
	if err != nil {
		return nil, err
	}
	out := *snapshot
	sourceChild := snapshot.Session.ParentID != ""
	if snapshot.SourceChild != nil {
		sourceChild = *snapshot.SourceChild
	}
	out.SourceChild = &sourceChild
	out.Session = row
	out.Messages = slices.Clone(snapshot.Messages)
	for i := range out.Messages {
		if out.Messages[i].SessionID != nativeID {
			return nil, fmt.Errorf("native history message identity mismatch: %w", sessioncatalog.ErrInvalid)
		}
		out.Messages[i].SessionID = row.ID
	}
	out.Parts = slices.Clone(snapshot.Parts)
	for i := range out.Parts {
		if out.Parts[i].SessionID != nativeID {
			return nil, fmt.Errorf("native history part identity mismatch: %w", sessioncatalog.ErrInvalid)
		}
		out.Parts[i].SessionID = row.ID
	}
	return &out, nil
}
