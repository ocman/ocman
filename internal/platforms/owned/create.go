package owned

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/google/uuid"
)

// CreationPendingError exposes only the Ocman recovery identity. The native
// binding, when known, stays in this integration's durable catalogue.
type CreationPendingError = platforms.CreationPendingError

func (a *Adapter) reserve(ctx context.Context, req platforms.CreateSessionRequest, operation ...any) (sessioncatalog.Session, bool, error) {
	if err := a.checkSource(ctx); err != nil {
		return sessioncatalog.Session{}, false, err
	}
	intent := req
	intent.Port = ""
	var rules []platforms.PermissionRule
	if req.Creation.Configured {
		rules = req.Creation.PermissionRules
	}
	// Preserve the receipt fingerprint used before creation moved into the
	// integration. Existing keyed requests must not acquire a second intent.
	raw, err := json.Marshal(struct {
		Request   platforms.CreateSessionRequest
		Rules     []platforms.PermissionRule
		Operation []any
	}{intent, rules, operation})
	if err != nil {
		return sessioncatalog.Session{}, false, err
	}
	hash := sha256.Sum256(raw)
	row, created, err := a.store.Reserve(ctx, sessioncatalog.Reservation{OwnerID: a.owner, Platform: string(a.ID()), Directory: req.Directory, Title: req.Title, ParentID: req.Creation.ParentID, Origin: sessioncatalog.Origin(req.Creation.Origin), OriginID: req.Creation.OriginID, RequestKey: req.Creation.RequestKey, PayloadHash: hex.EncodeToString(hash[:])})
	if err != nil {
		return row, false, err
	}
	if row.Purged {
		return row, false, platforms.ErrNotFound
	}
	if !created && row.CreationState != sessioncatalog.Ready {
		if row.CreationState == sessioncatalog.CreationFailed {
			return row, false, sessioncatalog.ErrCreationFailed
		}
		return row, false, &CreationPendingError{SessionID: row.ID, Err: sessioncatalog.ErrCreating}
	}
	if !created && row.SourceKey != a.source.SourceKey {
		return row, false, sessioncatalog.ErrBindingConflict
	}
	return row, created, nil
}

func creationIntent(in *platforms.CreationIdentity) *platforms.CreationIdentity {
	out := platforms.CreationIdentity{Origin: "user"}
	if in != nil {
		out = *in
	}
	if out.RequestKey == "" {
		out.RequestKey = uuid.NewString()
	}
	return &out
}

func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	req.Creation = creationIntent(req.Creation)
	if req.Creation.Configured {
		if _, ok := a.native.(platforms.SessionDisposer); !ok {
			return nil, platforms.ErrUnsupported
		}
	}
	row, created, err := a.reserve(ctx, req)
	if err != nil {
		return a.resumePlannedCreation(ctx, row, req, err)
	}
	if !created {
		return &platforms.CreateSessionResponse{ID: row.ID, ConfigurationApplied: req.Creation.Configured, Replayed: true}, nil
	}
	row, err = a.planCreation(ctx, row, &req)
	if err != nil {
		return nil, &CreationPendingError{SessionID: row.ID, Err: err}
	}
	response, err := a.native.CreateSession(ctx, req)
	return a.finishNativeCreation(ctx, row, response, err, req.Creation)
}

func (a *Adapter) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	nativeID, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	parent, err := a.store.Get(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	req.Creation = creationIntent(req.Creation)
	if req.Creation.Configured || len(req.Creation.PermissionRules) > 0 {
		return nil, sessioncatalog.ErrInvalid
	}
	if req.Creation.ParentID != "" && req.Creation.ParentID != req.SessionID {
		return nil, sessioncatalog.ErrInvalid
	}
	req.Creation.ParentID = req.SessionID
	fingerprint := req
	fingerprint.SessionID = nativeID
	row, created, err := a.reserve(ctx, platforms.CreateSessionRequest{Directory: parent.Directory, Creation: req.Creation}, fingerprint)
	if err != nil {
		return nil, err
	}
	if !created {
		return &platforms.CreateSessionResponse{ID: row.ID, Replayed: true}, nil
	}
	req.SessionID = nativeID
	response, err := a.native.ForkSession(ctx, req)
	return a.finishNativeCreation(ctx, row, response, err, req.Creation)
}

func (a *Adapter) finishNativeCreation(ctx context.Context, row sessioncatalog.Session, response *platforms.CreateSessionResponse, nativeErr error, intent *platforms.CreationIdentity) (*platforms.CreateSessionResponse, error) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	pending := func(err error) (*platforms.CreateSessionResponse, error) {
		return nil, &CreationPendingError{SessionID: row.ID, Err: err}
	}
	if nativeErr != nil {
		if errors.Is(nativeErr, platforms.ErrCreationRejected) {
			_, err := a.store.Fail(writeCtx, row.ID, nativeErr.Error())
			return nil, errors.Join(nativeErr, err)
		}
		return pending(nativeErr)
	}
	if response == nil || response.ID == "" {
		return pending(errors.New("platform returned no created session"))
	}
	if row.NativeID != "" && row.NativeID != response.ID {
		return pending(sessioncatalog.ErrBindingConflict)
	}
	if err := a.checkSource(writeCtx); err != nil {
		return pending(err)
	}
	if _, err := a.store.Attach(writeCtx, row.ID, a.binding(response.ID)); err != nil {
		return pending(err)
	}
	if intent.Configured && !response.ConfigurationApplied {
		err := a.native.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{SessionID: response.ID, Rules: intent.PermissionRules})
		if err != nil {
			disposer, ok := a.native.(platforms.SessionDisposer)
			if !ok {
				return pending(err)
			}
			if cleanupErr := disposer.DisposeSession(writeCtx, platforms.DisposeSessionRequest{SessionID: response.ID}); cleanupErr != nil {
				return pending(errors.Join(err, cleanupErr))
			}
			_, failureErr := a.store.Fail(writeCtx, row.ID, err.Error())
			return nil, errors.Join(err, failureErr)
		}
	}
	return &platforms.CreateSessionResponse{ID: row.ID, ConfigurationApplied: intent.Configured || response.ConfigurationApplied}, nil
}

// CommitCreation is called after application-owned linkage succeeds and before
// publishing the session. Configuring identities cannot be sent to or listed.
func (a *Adapter) CommitCreation(ctx context.Context, id string) error {
	if err := a.checkSource(ctx); err != nil {
		return err
	}
	row, err := a.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if row.Purged || row.OwnerID != a.owner || row.Platform != string(a.ID()) || row.SourceKey != a.source.SourceKey {
		return platforms.ErrNotFound
	}
	_, err = a.store.Bind(ctx, id, a.binding(row.NativeID))
	if err == nil {
		if err := a.recordStoredCreation(ctx, row); err != nil {
			return err
		}
		a.byID.Store(id, row.NativeID)
		a.flushLifecycle(ctx)
	}
	return err
}
