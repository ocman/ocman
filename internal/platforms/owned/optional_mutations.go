package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) Revert(ctx context.Context, req platforms.RevertSessionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	reverter, ok := a.native.(platforms.SessionReverter)
	if !ok {
		return platforms.ErrUnsupported
	}
	return reverter.Revert(ctx, req)
}
func (a *Adapter) Unrevert(ctx context.Context, req platforms.UnrevertSessionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	reverter, ok := a.native.(platforms.SessionReverter)
	if !ok {
		return platforms.ErrUnsupported
	}
	return reverter.Unrevert(ctx, req)
}
func (a *Adapter) NativeQueued(ctx context.Context, id string) ([]platforms.NativeQueuedMessage, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	queue, ok := a.native.(platforms.NativeQueue)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	return queue.NativeQueued(ctx, nativeID)
}
func (a *Adapter) CancelNativeQueued(ctx context.Context, req platforms.CancelNativeQueuedRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	queue, ok := a.native.(platforms.NativeQueue)
	if !ok {
		return platforms.ErrUnsupported
	}
	return queue.CancelNativeQueued(ctx, req)
}
func (a *Adapter) DisposeSession(ctx context.Context, req platforms.DisposeSessionRequest) error {
	if !sessioncatalog.IsID(req.SessionID) {
		return sessioncatalog.ErrInvalid
	}
	if err := a.checkSource(ctx); err != nil {
		return err
	}
	row, err := a.store.Get(ctx, req.SessionID)
	if err != nil {
		return err
	}
	if row.OwnerID != a.owner || row.Platform != string(a.ID()) || row.SourceKey != a.source.SourceKey {
		return platforms.ErrNotFound
	}
	if row.NativeID == "" {
		return sessioncatalog.ErrCreating
	}
	disposer, ok := a.native.(platforms.SessionDisposer)
	if !ok {
		return platforms.ErrUnsupported
	}
	req.SessionID = row.NativeID
	if err := disposer.DisposeSession(ctx, req); err != nil && !errors.Is(err, platforms.ErrNotFound) {
		return err
	}
	a.byID.Delete(row.ID)
	if row.CreationState == sessioncatalog.Configuring {
		_, err = a.store.Fail(ctx, row.ID, "created session was disposed before publication")
		return err
	}
	return a.store.Scrub(ctx, sessioncatalog.Source{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}, row.NativeID)
}
