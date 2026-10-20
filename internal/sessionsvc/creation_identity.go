package sessionsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/google/uuid"
)

type creationIdentities struct {
	store   *sessioncatalog.Store
	ownerID string
}

// WithSessionCatalogue is installed before serving requests. Remote calls carry
// intent to the owner; only that owner reserves and binds the identity.
func (s *Service) WithSessionCatalogue(store *sessioncatalog.Store, ownerID string) {
	s.identities = &creationIdentities{store: store, ownerID: ownerID}
}

type creationAttempt struct {
	identities *creationIdentities
	record     sessioncatalog.Session
	binding    sessioncatalog.Binding
}

// CreationUncertainError retains both identities when a native execution may
// exist. Callers must not automatically retry creation or remove its worktree.
type CreationUncertainError struct {
	OcmanID  string
	NativeID string
	Err      error
}

func (e *CreationUncertainError) Error() string {
	return fmt.Sprintf("session creation requires reconciliation (%s): %v", e.OcmanID, e.Err)
}
func (e *CreationUncertainError) Unwrap() error { return e.Err }

func prepareCreationIdentity(req *platforms.CreateSessionRequest, routineID string) error {
	if req.Creation == nil {
		req.Creation = &platforms.CreationIdentity{RequestKey: uuid.NewString(), Origin: string(sessioncatalog.User)}
	} else {
		copy := *req.Creation
		req.Creation = &copy
	}
	if routineID != "" {
		if req.Creation.Origin != string(sessioncatalog.User) &&
			(req.Creation.Origin != string(sessioncatalog.Routine) || req.Creation.OriginID != routineID) {
			return validation("creation origin does not match routine")
		}
		req.Creation.Origin, req.Creation.OriginID = string(sessioncatalog.Routine), routineID
	}
	return nil
}

func (s *Service) reserveCreation(ctx context.Context, p platforms.Platform, req platforms.CreateSessionRequest, rules []platforms.PermissionRule, operation ...any) (*creationAttempt, *platforms.CreateSessionResponse, error) {
	remoteID, _ := platforms.SplitPlatformID(string(p.ID()))
	if s.identities == nil || remoteID != "" {
		return nil, nil, nil
	}
	if s.identities.store == nil {
		return nil, nil, errors.New("session catalogue unavailable")
	}
	source, err := physicalSource(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	// Ports are runtime addresses and may change after restart. Rules and the
	// remaining request form the durable intent, without storing their content.
	intent := req
	intent.Port = ""
	payload, err := json.Marshal(struct {
		Request   platforms.CreateSessionRequest
		Rules     []platforms.PermissionRule
		Operation []any
	}{intent, rules, operation})
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(payload)
	row, created, err := s.identities.store.Reserve(ctx, sessioncatalog.Reservation{
		RequestKey: req.Creation.RequestKey, PayloadHash: hex.EncodeToString(digest[:]),
		OwnerID: s.identities.ownerID, Platform: string(p.ID()), Directory: req.Directory, Title: req.Title,
		ParentID: req.Creation.ParentID,
		Origin:   sessioncatalog.Origin(req.Creation.Origin), OriginID: req.Creation.OriginID,
	})
	if err != nil {
		return nil, nil, err
	}
	attempt := &creationAttempt{identities: s.identities, record: row, binding: sessioncatalog.Binding{
		OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: source.SourceKey,
	}}
	if created {
		return attempt, nil, nil
	}
	if row.CreationState == sessioncatalog.Ready {
		if row.SourceKey != source.SourceKey {
			return nil, nil, sessioncatalog.ErrBindingConflict
		}
		return nil, &platforms.CreateSessionResponse{ID: row.NativeID, ConfigurationApplied: rules != nil}, nil
	}
	if row.CreationState == sessioncatalog.CreationFailed {
		return nil, nil, sessioncatalog.ErrCreationFailed
	}
	return nil, nil, &CreationUncertainError{OcmanID: row.ID, NativeID: row.NativeID, Err: sessioncatalog.ErrCreating}
}

func (a *creationAttempt) uncertain(nativeID string, err error) error {
	if a == nil {
		return err
	}
	return &CreationUncertainError{OcmanID: a.record.ID, NativeID: nativeID, Err: err}
}

func (a *creationAttempt) nativeFailure(ctx context.Context, err error) error {
	if errors.Is(err, platforms.ErrCreationRejected) {
		return a.failed(ctx, err)
	}
	return a.uncertain("", err)
}

func (a *creationAttempt) bind(ctx context.Context, nativeID string) error {
	return a.recordNative(ctx, nativeID, true)
}

func (a *creationAttempt) attach(ctx context.Context, nativeID string) error {
	return a.recordNative(ctx, nativeID, false)
}

func (a *creationAttempt) recordNative(ctx context.Context, nativeID string, ready bool) error {
	if a == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	b := a.binding
	b.NativeID = nativeID
	var err error
	if ready {
		_, err = a.identities.store.Bind(ctx, a.record.ID, b)
	} else {
		_, err = a.identities.store.Attach(ctx, a.record.ID, b)
	}
	if err != nil {
		return a.uncertain(nativeID, err)
	}
	return nil
}

func (a *creationAttempt) failed(ctx context.Context, cause error) error {
	if a == nil {
		return cause
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := a.identities.store.Fail(ctx, a.record.ID, cause.Error())
	return errors.Join(cause, err)
}
