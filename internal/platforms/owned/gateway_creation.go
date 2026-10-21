package owned

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
)

// LegacyCreationUncertainError is retained for pre-cutover integration clients.
// Canonical adapters use platforms.CreationPendingError, which has no native ID.
type LegacyCreationUncertainError struct {
	OcmanID, NativeID string
	Err               error
}

func (e *LegacyCreationUncertainError) Error() string {
	return fmt.Sprintf("session creation requires reconciliation (%s): %v", e.OcmanID, e.Err)
}
func (e *LegacyCreationUncertainError) Unwrap() error { return e.Err }

type CreationAttempt struct {
	gateway *IdentityGateway
	record  sessioncatalog.Session
	binding sessioncatalog.Binding
}

func (g *IdentityGateway) Reserve(ctx context.Context, p platforms.Platform, req platforms.CreateSessionRequest, rules []platforms.PermissionRule, operation ...any) (*CreationAttempt, *platforms.CreateSessionResponse, error) {
	if _, canonical := p.(platforms.SessionCreationCommitter); canonical {
		return nil, nil, nil
	}
	remote, _ := platforms.SplitPlatformID(string(p.ID()))
	if g == nil || remote != "" {
		return nil, nil, nil
	}
	if g.store == nil {
		return nil, nil, errors.New("session catalogue unavailable")
	}
	source, err := physicalSource(ctx, p)
	if err != nil {
		return nil, nil, err
	}
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
	row, created, err := g.store.Reserve(ctx, sessioncatalog.Reservation{RequestKey: req.Creation.RequestKey, PayloadHash: hex.EncodeToString(digest[:]), OwnerID: g.owner, Platform: string(p.ID()), Directory: req.Directory, Title: req.Title, ParentID: req.Creation.ParentID, Origin: sessioncatalog.Origin(req.Creation.Origin), OriginID: req.Creation.OriginID})
	if err != nil {
		return nil, nil, err
	}
	attempt := &CreationAttempt{gateway: g, record: row, binding: sessioncatalog.Binding{OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: source.SourceKey}}
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
	return nil, nil, &LegacyCreationUncertainError{OcmanID: row.ID, NativeID: row.NativeID, Err: sessioncatalog.ErrCreating}
}

func (a *CreationAttempt) Uncertain(nativeID string, err error) error {
	if a == nil {
		return err
	}
	return &LegacyCreationUncertainError{OcmanID: a.record.ID, NativeID: nativeID, Err: err}
}
func (a *CreationAttempt) NativeFailure(ctx context.Context, err error) error {
	if errors.Is(err, platforms.ErrCreationRejected) {
		return a.Failed(ctx, err)
	}
	return a.Uncertain("", err)
}
func (a *CreationAttempt) Bind(ctx context.Context, nativeID string) error {
	return a.recordNative(ctx, nativeID, true)
}
func (a *CreationAttempt) Attach(ctx context.Context, nativeID string) error {
	return a.recordNative(ctx, nativeID, false)
}
func (a *CreationAttempt) recordNative(ctx context.Context, nativeID string, ready bool) error {
	if a == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	binding := a.binding
	binding.NativeID = nativeID
	var err error
	if ready {
		_, err = a.gateway.store.Bind(ctx, a.record.ID, binding)
	} else {
		_, err = a.gateway.store.Attach(ctx, a.record.ID, binding)
	}
	if err != nil {
		return a.Uncertain(nativeID, err)
	}
	return nil
}
func (a *CreationAttempt) Failed(ctx context.Context, cause error) error {
	if a == nil {
		return cause
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := a.gateway.store.Fail(ctx, a.record.ID, cause.Error())
	return errors.Join(cause, err)
}
