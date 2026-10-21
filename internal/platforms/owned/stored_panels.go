package owned

import (
	"context"
	"math"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) storedPanelData(ctx context.Context, id string) (*platforms.SessionDetail, platforms.StoredDetailProjector, error) {
	projector, ok := a.native.(platforms.StoredDetailProjector)
	if !ok {
		return nil, nil, platforms.ErrUnsupported
	}
	detail, err := a.storedConversation(ctx, id, math.MaxInt, 0)
	if err != nil {
		return nil, nil, err
	}
	identity, err := a.storedIdentity(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	copy := *detail.Session
	copy.ID = identity.NativeID
	detail.Session = &copy
	return detail, projector, nil
}

func (a *Adapter) storedChanges(ctx context.Context, id string) (*platforms.SessionChanges, error) {
	detail, projector, err := a.storedPanelData(ctx, id)
	if err != nil {
		return nil, err
	}
	changes, err := projector.ChangesFromStored(ctx, detail)
	if err == nil && changes != nil {
		changes.SessionID = id
	}
	return changes, err
}

func (a *Adapter) storedInfo(ctx context.Context, id string) (*platforms.SessionInfo, error) {
	detail, projector, err := a.storedPanelData(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := projector.InfoFromStored(ctx, detail)
	if err == nil && info != nil {
		info.SessionID = id
	}
	return info, err
}
