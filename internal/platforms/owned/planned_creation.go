package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) plannedSessionID(id string) string {
	if planner, ok := a.native.(platforms.SessionCreationPlanner); ok {
		return planner.PlannedSessionID(id)
	}
	return ""
}

func (a *Adapter) planCreation(ctx context.Context, row sessioncatalog.Session, req *platforms.CreateSessionRequest) (sessioncatalog.Session, error) {
	req.NativeID = a.plannedSessionID(row.ID)
	if req.NativeID == "" {
		return row, nil
	}
	attached, err := a.store.Attach(ctx, row.ID, a.binding(req.NativeID))
	if err != nil {
		return row, err
	}
	return attached, nil
}

// An explicit keyed retry may resume a planned creation, never an unknown
// legacy outcome or fork. Probe the exact durable ID before any native call.
// Configuration and application linkage still complete before publication.
func (a *Adapter) resumePlannedCreation(ctx context.Context, row sessioncatalog.Session, req platforms.CreateSessionRequest, original error) (*platforms.CreateSessionResponse, error) {
	if !errors.Is(original, sessioncatalog.ErrCreating) || row.Purged || row.CreationState != sessioncatalog.Configuring ||
		row.OwnerID != a.owner || row.SourceKey != a.source.SourceKey || row.NativeID == "" || row.NativeID != a.plannedSessionID(row.ID) {
		return nil, original
	}
	probe, ok := a.native.(platforms.SessionIdentityProbe)
	if !ok {
		return nil, original
	}
	metadata, err := probe.SessionIdentityMetadata(ctx, row.NativeID)
	if errors.Is(err, platforms.ErrNotFound) {
		// Crash before the POST, or a request still in flight. The same native
		// primary key makes either order safe; never choose a replacement ID.
		req.NativeID = row.NativeID
		response, createErr := a.native.CreateSession(ctx, req)
		return a.finishNativeCreation(ctx, row, response, createErr, req.Creation)
	}
	if err == nil && (metadata == nil || metadata.ID != row.NativeID || metadata.Directory != row.Directory) {
		err = sessioncatalog.ErrInvalid
	}
	if err != nil {
		return nil, &CreationPendingError{SessionID: row.ID, Err: err}
	}
	return a.finishNativeCreation(ctx, row, &platforms.CreateSessionResponse{ID: row.NativeID}, nil, req.Creation)
}
