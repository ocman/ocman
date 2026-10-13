package plugins

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

// SessionConfig is the host-owned, non-secret configuration admitted with a
// run. Providers cannot choose these values.
type SessionConfig struct {
	Project string
	Values  map[string]json.RawMessage
}

const (
	// SessionRunTimeout bounds a run, which may have to launch the project's
	// opencode instance before the first prompt.
	SessionRunTimeout = 2 * time.Minute
	// SessionCompletedTimeout bounds one outbound completed call, which must
	// not hold a plugin concurrency slot for long.
	SessionCompletedTimeout = 30 * time.Second
)

// SessionAuthorization must read current enablement, grants and the one
// configured project in the same critical section as revocation, exactly like
// ConversationAuthorization. Work already admitted cannot be recalled on
// revocation.
type SessionAuthorization func(ctx context.Context, pluginID string, use func(Description, []string, SessionConfig) error) error

// SessionStart starts a turn in the approved project: it creates the session
// with the profile's pinned rules, queues the prompt, and returns the session
// id immediately — completion arrives later through the outbox. dir is derived
// from the approved project (and a contained subpath); a plugin never names it.
type SessionStart func(ctx context.Context, pluginID string, config SessionConfig, params SessionRunParams, directory, operationID string) (string, error)

// SessionBroker is the only way a session plugin reaches a session, and the
// only way a completed turn reaches a plugin. It holds no state: a run either
// authorizes into the configured project or is denied.
type SessionBroker struct {
	authorize SessionAuthorization
	start     SessionStart
	call      ActionCall
}

func NewSessionBroker(authorize SessionAuthorization, start SessionStart, call ActionCall) *SessionBroker {
	return &SessionBroker{authorize: authorize, start: start, call: call}
}

// SessionAllowed fails closed on an undeclared capability or a missing grant,
// so an enabled plugin without the approved grant reaches nothing. It is
// exported so the host can ask the same question before it schedules delivery,
// rather than discovering the denial by attempting it.
func SessionAllowed(d Description, grants []string) error {
	if !hasSession(d.Capabilities) {
		return &WireError{Category: ErrorNotFound}
	}
	if !slices.Contains(grants, SessionRunGrant) {
		return &WireError{Category: ErrorPermissionDenied}
	}
	return nil
}

// Run admits a plugin-initiated run and starts the turn. The host's start hook
// runs outside the authorization critical section: it creates sessions and
// launches processes, which must not block plugin management. The subpath is
// resolved here, against the approved project, so a traversal or absolute path
// is denied before any host work. The returned session id is the plugin's first
// knowledge of the session, before any completed-turn delivery.
func (b *SessionBroker) Run(ctx context.Context, pluginID string, call Call) (SessionRunResult, error) {
	if err := ctx.Err(); err != nil {
		return SessionRunResult{}, err
	}
	if call.Capability != SessionCapability.Name || call.Version != SessionCapability.Version || call.Method != SessionRunMethod {
		return SessionRunResult{}, &WireError{Category: ErrorNotFound}
	}
	if !validText(call.OperationID) {
		return SessionRunResult{}, &WireError{Category: ErrorInvalidArgument}
	}
	params, err := DecodeSessionRunParams(call.Params)
	if err != nil {
		return SessionRunResult{}, &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, SessionRunTimeout)
	defer cancel()
	var config SessionConfig
	var directory string
	if err := b.authorize(ctx, pluginID, func(d Description, grants []string, admitted SessionConfig) error {
		if err := SessionAllowed(d, grants); err != nil {
			return err
		}
		if admitted.Project == "" {
			return &WireError{Category: ErrorPermissionDenied}
		}
		resolved, ok := SessionDirectoryFor(admitted.Project, params.Subpath)
		if !ok {
			return &WireError{Category: ErrorPermissionDenied}
		}
		config, directory = admitted, resolved
		return nil
	}); err != nil {
		return SessionRunResult{}, err
	}
	sessionID, err := b.start(ctx, pluginID, config, params, directory, call.OperationID)
	if err != nil {
		return SessionRunResult{}, err
	}
	if sessionID == "" {
		return SessionRunResult{}, ErrInvalidMessage
	}
	return SessionRunResult{SessionID: sessionID}, nil
}

// Completed delivers one completed assistant turn to the plugin that started
// the session. operationID is the host's idempotency key; the durable outbox
// row is this call's receipt, stable across retries, so a plugin can recognize
// a repeat. The plugin must acknowledge with an empty object.
func (b *SessionBroker) Completed(ctx context.Context, pluginID, operationID string, completion SessionCompleted) error {
	if completion.Validate() != nil || !validText(operationID) {
		return &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, SessionCompletedTimeout)
	defer cancel()
	var replies <-chan Reply
	if err := b.authorize(ctx, pluginID, func(d Description, grants []string, _ SessionConfig) error {
		if err := SessionAllowed(d, grants); err != nil {
			return err
		}
		params, err := json.Marshal(completion)
		if err != nil {
			return err
		}
		deadline, _ := ctx.Deadline()
		replies, err = b.call(ctx, pluginID, Call{
			OperationID: operationID, Capability: SessionCapability.Name, Version: SessionCapability.Version,
			Method: SessionCompletedMethod, DeadlineUnixMS: deadline.UnixMilli(), Params: params,
		})
		return err
	}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r, ok := <-replies:
		switch {
		case !ok:
			return ErrUnavailable
		case r.Err != nil:
			return r.Err
		// session.v1 completions are unary, even though the transport streams.
		case r.Message.Type != TypeResult || r.Message.Result == nil:
			return ErrInvalidMessage
		case r.Message.Result.Error != nil:
			return r.Message.Result.Error
		}
		var ack map[string]json.RawMessage
		if json.Unmarshal(r.Message.Result.Value, &ack) != nil || ack == nil || len(ack) != 0 {
			return ErrInvalidMessage
		}
		return nil
	}
}
